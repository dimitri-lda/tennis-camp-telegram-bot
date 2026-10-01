package main

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Dialog and ticket state lives in memory only, so it is lost on restart.

type dialogStage int

const (
	stageIdle dialogStage = iota
	stageAwaitingQuestion
	stageAwaitingOperatorQuestion
)

type ticketStatus int

const (
	ticketOpen ticketStatus = iota
	ticketTaken
	ticketClosed
)

const (
	sessionRoleUser      = "user"
	sessionRoleAssistant = "assistant"
	sessionRoleOperator  = "operator"

	sectionGeneral  = ""
	sectionAbout    = "О Dzala"
	sectionTraining = "Тренировки в Тбилиси"
	sectionCamps    = "Кэмпы"
)

type sessionMessage struct {
	role string
	text string
}

type chatState struct {
	section          string
	topic            string
	campID           string
	stage            dialogStage
	lastQuestion     string
	lastAnswer       string
	ticketID         int64
	sessionID        int64
	sessionActive    bool
	startedAt        time.Time
	lastActivityAt   time.Time
	lastAIQuestionAt time.Time
	aiQuestionCount  int
	aiInFlight       bool
	aiCancel         context.CancelFunc
	auditMessageID   int
	auditCard        string
	history          []sessionMessage
}

type ticket struct {
	id            int64
	clientChatID  int64
	section       string
	topic         string
	campTitle     string
	question      string
	aiAnswer      string
	card          string
	cardMessageID int
	operatorID    int64
	operatorName  string
	status        ticketStatus
	history       []sessionMessage
}

type sessionClosure struct {
	chatID         int64
	sessionID      int64
	startedAt      time.Time
	endedAt        time.Time
	auditMessageID int
	auditCard      string
	history        []sessionMessage
	ticket         ticket
	hadTicket      bool
}

type aiStartResult int

const (
	aiStartOK aiStartResult = iota
	aiStartBusy
	aiStartTooSoon
	aiStartLimit
	aiStartNoSession
)

type takeResult int

const (
	takeAssigned takeResult = iota
	takeAlreadyOwned
	takeTakenByOther
	takeClosed
	takeUnknown
)

type closeResult int

const (
	closeDone closeResult = iota
	closeForbidden
	closeAlreadyClosed
	closeUnknown
)

type replyResult int

const (
	replyAllowed replyResult = iota
	replyNotTaken
	replyNotOwner
	replyClosed
	replyUnknown
)

type botStats struct {
	sessionsStarted atomic.Uint64
	aiRequests      atomic.Uint64
	aiSuccess       atomic.Uint64
	aiFailure       atomic.Uint64
	handoffs        atomic.Uint64
	timeouts        atomic.Uint64
	busyRejected    atomic.Uint64
	throttled       atomic.Uint64
}

type store struct {
	mu            sync.Mutex
	chats         map[int64]chatState
	tickets       map[int64]ticket
	groupMessages map[int]int64
	lastSessionID int64
	lastTicketID  int64
}

func newStore() *store {
	return &store{
		chats:         make(map[int64]chatState),
		tickets:       make(map[int64]ticket),
		groupMessages: make(map[int]int64),
	}
}

func (s *store) chat(chatID int64) chatState {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.chats[chatID]
	state.history = cloneHistory(state.history)
	state.aiCancel = nil
	return state
}

func (s *store) startSession(chatID int64) int64 {
	return s.startSessionAt(chatID, time.Now())
}

func (s *store) startSessionAt(chatID int64, now time.Time) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current := s.chats[chatID]; current.aiCancel != nil {
		current.aiCancel()
	}
	s.lastSessionID++
	s.chats[chatID] = chatState{
		sessionID:      s.lastSessionID,
		sessionActive:  true,
		startedAt:      now,
		lastActivityAt: now,
	}
	return s.lastSessionID
}

func (s *store) sessionActive(chatID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chats[chatID].sessionActive
}

func (s *store) appendHistory(chatID int64, role, text string) {
	s.appendHistoryAt(chatID, role, text, time.Now())
}

func (s *store) appendHistoryAt(chatID int64, role, text string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.chats[chatID]
	if !ok || !state.sessionActive {
		return
	}
	state.lastActivityAt = now
	state.history = append(state.history, sessionMessage{role: role, text: text})
	s.chats[chatID] = state
}

func (s *store) sessionHistory(chatID int64) []sessionMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneHistory(s.chats[chatID].history)
}

func (s *store) endSession(chatID int64) (ticket, bool) {
	closure := s.closeSession(chatID)
	return closure.ticket, closure.hadTicket
}

func (s *store) closeSession(chatID int64) sessionClosure {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeSessionLocked(chatID)
}

func (s *store) closeSessionIfCurrent(chatID, sessionID int64) (sessionClosure, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.chats[chatID]
	if !ok || state.sessionID != sessionID {
		return sessionClosure{}, false
	}
	return s.closeSessionLocked(chatID), true
}

func (s *store) closeSessionLocked(chatID int64) sessionClosure {
	state, ok := s.chats[chatID]
	if !ok {
		return sessionClosure{chatID: chatID}
	}
	if state.aiCancel != nil {
		state.aiCancel()
	}
	closure := sessionClosure{
		chatID:         chatID,
		sessionID:      state.sessionID,
		startedAt:      state.startedAt,
		endedAt:        time.Now(),
		auditMessageID: state.auditMessageID,
		auditCard:      state.auditCard,
		history:        cloneHistory(state.history),
	}
	current, hasTicket := s.tickets[state.ticketID]
	if hasTicket && current.status != ticketClosed {
		current.status = ticketClosed
		current.history = cloneHistory(state.history)
		s.tickets[current.id] = current
		closure.ticket = current
		closure.hadTicket = true
	}
	delete(s.chats, chatID)
	return closure
}

func cloneHistory(history []sessionMessage) []sessionMessage {
	return append([]sessionMessage(nil), history...)
}

func (s *store) beginAI(chatID int64, now time.Time, minInterval time.Duration, maxQuestions int, cancel context.CancelFunc) (chatState, aiStartResult) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.chats[chatID]
	if !ok || !state.sessionActive {
		return chatState{}, aiStartNoSession
	}
	if state.aiInFlight {
		return chatState{}, aiStartBusy
	}
	if maxQuestions > 0 && state.aiQuestionCount >= maxQuestions {
		return chatState{}, aiStartLimit
	}
	if !state.lastAIQuestionAt.IsZero() && now.Sub(state.lastAIQuestionAt) < minInterval {
		return chatState{}, aiStartTooSoon
	}
	state.aiInFlight = true
	state.aiCancel = cancel
	state.lastAIQuestionAt = now
	state.lastActivityAt = now
	state.aiQuestionCount++
	s.chats[chatID] = state
	state.history = cloneHistory(state.history)
	state.aiCancel = nil
	return state, aiStartOK
}

func (s *store) finishAI(chatID, sessionID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.chats[chatID]
	if !ok || !state.sessionActive || state.sessionID != sessionID || !state.aiInFlight {
		return false
	}
	state.aiInFlight = false
	state.aiCancel = nil
	s.chats[chatID] = state
	return true
}

func (s *store) registerAuditCard(chatID, sessionID int64, messageID int, card string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.chats[chatID]
	if !ok || state.sessionID != sessionID || state.auditMessageID != 0 {
		return false
	}
	state.auditMessageID = messageID
	state.auditCard = card
	s.chats[chatID] = state
	return true
}

func (s *store) expireSessionIfIdle(chatID, sessionID int64, now time.Time, aiTimeout, operatorTimeout time.Duration) (sessionClosure, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.chats[chatID]
	if !ok || state.sessionID != sessionID || state.lastActivityAt.IsZero() {
		return sessionClosure{}, false
	}
	timeout := aiTimeout
	if current, ok := s.tickets[state.ticketID]; ok && current.status == ticketTaken {
		timeout = operatorTimeout
	}
	if timeout <= 0 || now.Sub(state.lastActivityAt) < timeout {
		return sessionClosure{}, false
	}
	return s.closeSessionLocked(chatID), true
}

func (s *store) expiredChatIDs(now time.Time, aiTimeout, operatorTimeout time.Duration) []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]int64, 0)
	for chatID, state := range s.chats {
		if !state.sessionActive || state.lastActivityAt.IsZero() {
			continue
		}
		timeout := aiTimeout
		if current, ok := s.tickets[state.ticketID]; ok && current.status == ticketTaken {
			timeout = operatorTimeout
		}
		if timeout > 0 && now.Sub(state.lastActivityAt) >= timeout {
			ids = append(ids, chatID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (s *store) touch(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.chats[chatID]
	if !ok {
		return
	}
	state.lastActivityAt = time.Now()
	s.chats[chatID] = state
}

func (s *store) update(chatID int64, mutate func(*chatState)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.chats[chatID]
	mutate(&state)
	s.chats[chatID] = state
}

func (s *store) selectSection(chatID int64, section string) {
	s.update(chatID, func(state *chatState) {
		state.section = section
		state.topic = ""
		state.campID = ""
		state.stage = stageIdle
		state.lastQuestion = ""
		state.lastAnswer = ""
		state.lastActivityAt = time.Now()
	})
}

func (s *store) selectCamp(chatID int64, campID string) {
	s.update(chatID, func(state *chatState) {
		state.section = sectionCamps
		state.topic = ""
		state.campID = campID
		state.stage = stageIdle
		state.lastQuestion = ""
		state.lastAnswer = ""
		state.lastActivityAt = time.Now()
	})
}

func (s *store) selectTopic(chatID int64, section, topic, campID string) {
	s.update(chatID, func(state *chatState) {
		state.section = section
		state.topic = topic
		state.campID = campID
		state.stage = stageIdle
		state.lastQuestion = ""
		state.lastAnswer = ""
		state.lastActivityAt = time.Now()
	})
}

func (s *store) createTicket(clientChatID int64, campTitle, question, aiAnswer string) (ticket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := s.chats[clientChatID]
	if current, ok := s.tickets[state.ticketID]; ok && current.status != ticketClosed {
		return current, false
	}

	s.lastTicketID++
	created := ticket{
		id:            s.lastTicketID,
		clientChatID:  clientChatID,
		section:       state.section,
		topic:         state.topic,
		campTitle:     campTitle,
		question:      question,
		aiAnswer:      aiAnswer,
		cardMessageID: state.auditMessageID,
		status:        ticketOpen,
		history:       cloneHistory(state.history),
	}
	s.tickets[created.id] = created

	state.ticketID = created.id
	state.stage = stageIdle
	state.sessionActive = true
	state.lastQuestion = question
	state.lastAnswer = aiAnswer
	s.chats[clientChatID] = state

	return created, true
}

// registerCard remembers the operator group message that represents the ticket.
func (s *store) registerCard(ticketID int64, messageID int, card string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.tickets[ticketID]
	if !ok {
		return
	}
	current.card = card
	current.cardMessageID = messageID
	s.tickets[ticketID] = current
	s.groupMessages[messageID] = ticketID
}

// linkGroupMessage attaches a relayed client message to its ticket so that an
// operator reply to that message is routed back to the right client.
func (s *store) linkGroupMessage(ticketID int64, messageID int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tickets[ticketID]; ok {
		s.groupMessages[messageID] = ticketID
	}
}

func (s *store) dropTicket(ticketID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.tickets[ticketID]
	if !ok {
		return
	}
	delete(s.tickets, ticketID)
	if current.cardMessageID != 0 {
		delete(s.groupMessages, current.cardMessageID)
	}
	state := s.chats[current.clientChatID]
	if state.ticketID == ticketID {
		state.ticketID = 0
		s.chats[current.clientChatID] = state
	}
}

// activeTicket returns the ticket the client currently talks to an operator through.
func (s *store) activeTicket(clientChatID int64) (ticket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.chats[clientChatID]
	if state.ticketID == 0 {
		return ticket{}, false
	}
	current, ok := s.tickets[state.ticketID]
	if !ok || current.status == ticketClosed {
		return ticket{}, false
	}
	return current, true
}

// takeTicket assigns the first operator who presses the button.
func (s *store) takeTicket(ticketID, operatorID int64, operatorName string) (ticket, takeResult) {
	s.mu.Lock()
	defer s.mu.Unlock()

	current, ok := s.tickets[ticketID]
	if !ok {
		return ticket{}, takeUnknown
	}
	switch {
	case current.status == ticketClosed:
		return current, takeClosed
	case current.operatorID == operatorID:
		return current, takeAlreadyOwned
	case current.operatorID != 0:
		return current, takeTakenByOther
	}

	current.operatorID = operatorID
	current.operatorName = operatorName
	current.status = ticketTaken
	s.tickets[ticketID] = current
	return current, takeAssigned
}

func (s *store) ticketByID(ticketID int64) (ticket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.tickets[ticketID]
	return current, ok
}

// closeTicket may be closed by the assigned operator, or by anyone while unassigned.
func (s *store) closeTicket(ticketID, operatorID int64) (ticket, closeResult) {
	s.mu.Lock()
	defer s.mu.Unlock()

	current, ok := s.tickets[ticketID]
	if !ok {
		return ticket{}, closeUnknown
	}
	if current.status == ticketClosed {
		return current, closeAlreadyClosed
	}
	if current.operatorID != 0 && current.operatorID != operatorID {
		return current, closeForbidden
	}

	current.status = ticketClosed
	s.tickets[ticketID] = current

	state := s.chats[current.clientChatID]
	if state.ticketID == ticketID {
		state.ticketID = 0
		state.stage = stageIdle
		s.chats[current.clientChatID] = state
	}
	return current, closeDone
}

// operatorReplyTarget resolves which ticket an operator reply belongs to.
func (s *store) operatorReplyTarget(groupMessageID int, operatorID int64) (ticket, replyResult) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ticketID, ok := s.groupMessages[groupMessageID]
	if !ok {
		return ticket{}, replyUnknown
	}
	current, ok := s.tickets[ticketID]
	if !ok {
		return ticket{}, replyUnknown
	}
	switch {
	case current.status == ticketClosed:
		return current, replyClosed
	case current.operatorID == 0:
		return current, replyNotTaken
	case current.operatorID != operatorID:
		return current, replyNotOwner
	}
	return current, replyAllowed
}
