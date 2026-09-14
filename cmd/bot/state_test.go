package main

import (
	"sync"
	"testing"
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
