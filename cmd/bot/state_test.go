package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestSelectCampRemembersChoice(t *testing.T) {
	state := newStore()
	state.update(7, func(chat *chatState) {
		chat.stage = stageAwaitingQuestion
		chat.lastQuestion = "Старый вопрос"
		chat.lastAnswer = "Старый ответ"
	})

	state.selectCamp(7, "tbilisi")

	chat := state.chat(7)
	if chat.campID != "tbilisi" {
		t.Errorf("campID = %q, want %q", chat.campID, "tbilisi")
	}
	if chat.section != sectionCamps {
		t.Errorf("section = %q, want %q", chat.section, sectionCamps)
	}
	if chat.stage != stageIdle {
		t.Errorf("stage = %d, want stageIdle", chat.stage)
	}
	if chat.lastQuestion != "" || chat.lastAnswer != "" {
		t.Errorf("previous context = %q, %q, want both empty", chat.lastQuestion, chat.lastAnswer)
	}
	if got := state.chat(8).campID; got != "" {
		t.Errorf("campID of another chat = %q, want an empty string", got)
	}
}

func TestSelectSectionClearsCampContext(t *testing.T) {
	state := newStore()
	state.selectTopic(7, sectionCamps, "Программа", "tbilisi")
	state.selectSection(7, sectionTraining)

	chat := state.chat(7)
	if chat.section != sectionTraining || chat.topic != "" || chat.campID != "" {
		t.Errorf("chat = %+v, want training section without a topic or selected camp", chat)
	}

	state.selectSection(7, sectionGeneral)
	if chat = state.chat(7); chat.section != "" || chat.topic != "" || chat.campID != "" {
		t.Errorf("chat = %+v, want general context without a topic or selected camp", chat)
	}
}

func TestSelectTopicIsCopiedToTicket(t *testing.T) {
	state := newStore()
	state.selectTopic(7, sectionTraining, "Падел", "")

	created, ok := state.createTicket(7, "", "Когда есть занятия?", "")
	if !ok {
		t.Fatal("createTicket() created = false, want true")
	}
	if created.section != sectionTraining || created.topic != "Падел" || created.campTitle != "" {
		t.Errorf("ticket = %+v, want the selected training topic", created)
	}
}

func TestSessionLifecycleKeepsCompleteHistory(t *testing.T) {
	state := newStore()
	state.startSession(7)
	state.appendHistory(7, sessionRoleUser, "Первый вопрос")
	state.appendHistory(7, sessionRoleAssistant, "Первый ответ")
	state.appendHistory(7, sessionRoleUser, "Второй вопрос")

	history := state.sessionHistory(7)
	if !state.sessionActive(7) {
		t.Fatal("sessionActive() = false, want true")
	}
	if len(history) != 3 || history[0].text != "Первый вопрос" || history[2].text != "Второй вопрос" {
		t.Errorf("sessionHistory() = %+v, want all three messages", history)
	}
	history[0].text = "изменено"
	if state.sessionHistory(7)[0].text != "Первый вопрос" {
		t.Error("sessionHistory() returned mutable store data")
	}

	created, ok := state.createTicket(7, "Тбилиси, Грузия", "Второй вопрос", "")
	if !ok || len(created.history) != 3 {
		t.Errorf("createTicket() history = %+v, want complete session history", created.history)
	}
	closed, hadTicket := state.endSession(7)
	if !hadTicket || closed.status != ticketClosed {
		t.Errorf("endSession() = %+v, %t, want a closed ticket", closed, hadTicket)
	}
	if state.sessionActive(7) || len(state.sessionHistory(7)) != 0 {
		t.Error("endSession() did not clear the client session")
	}
}

func TestSessionIDsAndAIGuards(t *testing.T) {
	state := newStore()
	now := time.Date(2026, time.October, 1, 10, 0, 0, 0, time.UTC)
	firstSession := state.startSessionAt(7, now)
	if firstSession == 0 {
		t.Fatal("startSessionAt() returned a zero session ID")
	}

	ctx, cancel := context.WithCancel(context.Background())
	snapshot, result := state.beginAI(7, now, 5*time.Second, 2, cancel)
	if result != aiStartOK || snapshot.sessionID != firstSession {
		t.Fatalf("beginAI() = %+v, %d, want current session and aiStartOK", snapshot, result)
	}
	if _, result = state.beginAI(7, now, 5*time.Second, 2, func() {}); result != aiStartBusy {
		t.Errorf("second beginAI() = %d, want aiStartBusy", result)
	}
	if !state.finishAI(7, firstSession) {
		t.Fatal("finishAI() = false for current session")
	}
	if _, result = state.beginAI(7, now.Add(2*time.Second), 5*time.Second, 2, func() {}); result != aiStartTooSoon {
		t.Errorf("early beginAI() = %d, want aiStartTooSoon", result)
	}
	if _, result = state.beginAI(7, now.Add(6*time.Second), 5*time.Second, 2, func() {}); result != aiStartOK {
		t.Fatalf("second allowed beginAI() = %d, want aiStartOK", result)
	}
	state.finishAI(7, firstSession)
	if _, result = state.beginAI(7, now.Add(12*time.Second), 5*time.Second, 2, func() {}); result != aiStartLimit {
		t.Errorf("third beginAI() = %d, want aiStartLimit", result)
	}

	cancel()
	secondSession := state.startSessionAt(7, now.Add(time.Hour))
	if secondSession == firstSession {
		t.Error("new session reused the previous session ID")
	}
	if state.finishAI(7, firstSession) {
		t.Error("finishAI() accepted a stale session ID")
	}
	_ = ctx
}

func TestEndSessionCancelsInFlightAI(t *testing.T) {
	state := newStore()
	now := time.Now()
	sessionID := state.startSessionAt(7, now)
	ctx, cancel := context.WithCancel(context.Background())
	if _, result := state.beginAI(7, now, 0, 10, cancel); result != aiStartOK {
		t.Fatalf("beginAI() = %d, want aiStartOK", result)
	}

	closure := state.closeSession(7)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("closeSession() did not cancel the in-flight AI request")
	}
	if closure.sessionID != sessionID || state.finishAI(7, sessionID) {
		t.Errorf("closure = %+v; stale AI request remained valid", closure)
	}
}

func TestExpiredSessionsUseDifferentTimeouts(t *testing.T) {
	state := newStore()
	now := time.Date(2026, time.October, 1, 10, 0, 0, 0, time.UTC)
	state.startSessionAt(1, now)
	state.startSessionAt(2, now)
	ticket, _ := state.createTicket(2, "", "Вопрос", "")
	state.takeTicket(ticket.id, 11, "Оператор")

	expired := state.expiredChatIDs(now.Add(25*time.Hour), 24*time.Hour, 48*time.Hour)
	if len(expired) != 1 || expired[0] != 1 {
		t.Errorf("expiredChatIDs(25h) = %v, want only AI session 1", expired)
	}
	expired = state.expiredChatIDs(now.Add(49*time.Hour), 24*time.Hour, 48*time.Hour)
	if len(expired) != 2 {
		t.Errorf("expiredChatIDs(49h) = %v, want both sessions", expired)
	}
}

func TestAuditCardIsReusedByOperatorTicket(t *testing.T) {
	state := newStore()
	sessionID := state.startSessionAt(7, time.Now())
	state.appendHistory(7, sessionRoleUser, "Первый вопрос")
	if !state.registerAuditCard(7, sessionID, 500, "AI-сессия") {
		t.Fatal("registerAuditCard() = false, want true")
	}

	created, ok := state.createTicket(7, "", "Первый вопрос", "")
	if !ok || created.cardMessageID != 500 {
		t.Errorf("createTicket() = %+v, %t, want audit message 500 reused", created, ok)
	}
	closure := state.closeSession(7)
	if !closure.hadTicket || closure.ticket.id != created.id || closure.auditMessageID != 500 || len(closure.history) != 1 {
		t.Errorf("closeSession() = %+v, want ticket, audit card and transcript", closure)
	}
}

func TestCreateTicketIsAtomicPerClient(t *testing.T) {
	state := newStore()
	const attempts = 32

	start := make(chan struct{})
	results := make(chan bool, attempts)
	var group sync.WaitGroup
	for range attempts {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, created := state.createTicket(100, "Тбилиси, Грузия", "Вопрос", "")
			results <- created
		}()
	}
	close(start)
	group.Wait()
	close(results)

	createdCount := 0
	for created := range results {
		if created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Errorf("created tickets = %d, want 1", createdCount)
	}
	if current, active := state.activeTicket(100); !active || current.id != 1 {
		t.Errorf("activeTicket() = %+v, %t, want ticket 1", current, active)
	}
}

func TestFirstOperatorTakesTicket(t *testing.T) {
	state := newStore()
	created, _ := state.createTicket(100, "Тбилиси, Грузия", "Как проходит день?", "Ответ ИИ")

	taken, result := state.takeTicket(created.id, 11, "Оксана")
	if result != takeAssigned {
		t.Fatalf("takeTicket() result = %d, want takeAssigned", result)
	}
	if taken.operatorID != 11 || taken.operatorName != "Оксана" || taken.status != ticketTaken {
		t.Errorf("takeTicket() = %+v, want the ticket assigned to operator 11", taken)
	}
}

func TestSecondOperatorCannotTakeTicket(t *testing.T) {
	state := newStore()
	created, _ := state.createTicket(100, "Тбилиси, Грузия", "Как проходит день?", "")
	if _, result := state.takeTicket(created.id, 11, "Оксана"); result != takeAssigned {
		t.Fatalf("takeTicket() result = %d, want takeAssigned", result)
	}

	current, result := state.takeTicket(created.id, 22, "Натела")
	if result != takeTakenByOther {
		t.Errorf("takeTicket() result = %d, want takeTakenByOther", result)
	}
	if current.operatorID != 11 {
		t.Errorf("operatorID = %d, want the first operator 11", current.operatorID)
	}

	if _, result := state.takeTicket(created.id, 11, "Оксана"); result != takeAlreadyOwned {
		t.Errorf("takeTicket() result = %d, want takeAlreadyOwned", result)
	}
	if _, result := state.takeTicket(created.id+1, 11, "Оксана"); result != takeUnknown {
		t.Errorf("takeTicket() result = %d, want takeUnknown", result)
	}
}

func TestCloseTicket(t *testing.T) {
	state := newStore()
	created, _ := state.createTicket(100, "Тбилиси, Грузия", "Как проходит день?", "")
	state.takeTicket(created.id, 11, "Оксана")

	if _, result := state.closeTicket(created.id, 22); result != closeForbidden {
		t.Errorf("closeTicket() result = %d, want closeForbidden for another operator", result)
	}

	closed, result := state.closeTicket(created.id, 11)
	if result != closeDone {
		t.Fatalf("closeTicket() result = %d, want closeDone", result)
	}
	if closed.clientChatID != 100 {
		t.Errorf("clientChatID = %d, want 100", closed.clientChatID)
	}
	if _, active := state.activeTicket(100); active {
		t.Error("activeTicket() = true, want false after closing")
	}
	if _, result := state.closeTicket(created.id, 11); result != closeAlreadyClosed {
		t.Errorf("closeTicket() result = %d, want closeAlreadyClosed", result)
	}
}

func TestUnassignedTicketIsClosedByAnyOperator(t *testing.T) {
	state := newStore()
	created, _ := state.createTicket(100, "", "Вопрос", "")

	if _, result := state.closeTicket(created.id, 33); result != closeDone {
		t.Errorf("closeTicket() result = %d, want closeDone while unassigned", result)
	}
}

func TestOperatorReplyRouting(t *testing.T) {
	state := newStore()
	created, _ := state.createTicket(100, "Тбилиси, Грузия", "Вопрос", "")
	state.registerCard(created.id, 500, "Новая заявка")
	state.linkGroupMessage(created.id, 501)

	if _, result := state.operatorReplyTarget(500, 11); result != replyNotTaken {
		t.Errorf("operatorReplyTarget() result = %d, want replyNotTaken", result)
	}
	state.takeTicket(created.id, 11, "Оксана")

	target, result := state.operatorReplyTarget(501, 11)
	if result != replyAllowed {
		t.Fatalf("operatorReplyTarget() result = %d, want replyAllowed", result)
	}
	if target.clientChatID != 100 {
		t.Errorf("clientChatID = %d, want 100", target.clientChatID)
	}

	if _, result := state.operatorReplyTarget(501, 22); result != replyNotOwner {
		t.Errorf("operatorReplyTarget() result = %d, want replyNotOwner", result)
	}
	if _, result := state.operatorReplyTarget(999, 11); result != replyUnknown {
		t.Errorf("operatorReplyTarget() result = %d, want replyUnknown", result)
	}

	state.closeTicket(created.id, 11)
	if _, result := state.operatorReplyTarget(501, 11); result != replyClosed {
		t.Errorf("operatorReplyTarget() result = %d, want replyClosed", result)
	}
}

func TestActiveTicketFollowsTheLatestHandoff(t *testing.T) {
	state := newStore()
	first, _ := state.createTicket(100, "", "Первый вопрос", "")
	state.closeTicket(first.id, 11)

	second, _ := state.createTicket(100, "", "Второй вопрос", "")
	current, active := state.activeTicket(100)
	if !active {
		t.Fatal("activeTicket() = false, want true")
	}
	if current.id != second.id {
		t.Errorf("ticket id = %d, want %d", current.id, second.id)
	}

	state.dropTicket(second.id)
	if _, active := state.activeTicket(100); active {
		t.Error("activeTicket() = true, want false after dropTicket")
	}
}
