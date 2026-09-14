package main

import "sync"

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
)

type sessionMessage struct {
	role string
	text string
}

type chatState struct {
	campID        string
	stage         dialogStage
	lastQuestion  string
	lastAnswer    string
	ticketID      int64
	sessionActive bool
	history       []sessionMessage
}

type ticket struct {
	id            int64
	clientChatID  int64
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

type store struct {
	mu            sync.Mutex
	chats         map[int64]chatState
	tickets       map[int64]ticket
	groupMessages map[int]int64
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
	return state
}

func (s *store) startSession(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chats[chatID] = chatState{sessionActive: true}
}

func (s *store) sessionActive(chatID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chats[chatID].sessionActive
}

func (s *store) appendHistory(chatID int64, role, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.chats[chatID]
	state.sessionActive = true
	state.history = append(state.history, sessionMessage{role: role, text: text})
	s.chats[chatID] = state
}

func (s *store) sessionHistory(chatID int64) []sessionMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneHistory(s.chats[chatID].history)
}

func (s *store) endSession(chatID int64) (ticket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := s.chats[chatID]
	current, hasTicket := s.tickets[state.ticketID]
	if hasTicket && current.status != ticketClosed {
		current.status = ticketClosed
		s.tickets[current.id] = current
	} else {
		hasTicket = false
	}
	delete(s.chats, chatID)
	return current, hasTicket
}

func cloneHistory(history []sessionMessage) []sessionMessage {
	return append([]sessionMessage(nil), history...)
}

func (s *store) update(chatID int64, mutate func(*chatState)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.chats[chatID]
	mutate(&state)
	s.chats[chatID] = state
}

func (s *store) selectCamp(chatID int64, campID string) {
	s.update(chatID, func(state *chatState) {
		state.campID = campID
		state.stage = stageIdle
		state.lastQuestion = ""
		state.lastAnswer = ""
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
		id:           s.lastTicketID,
		clientChatID: clientChatID,
		campTitle:    campTitle,
		question:     question,
		aiAnswer:     aiAnswer,
		status:       ticketOpen,
		history:      cloneHistory(state.history),
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
