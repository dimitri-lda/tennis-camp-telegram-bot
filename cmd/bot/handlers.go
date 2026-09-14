package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const (
	campCallbackPrefix        = "camp:"
	campInfoCallbackPrefix    = "camp_info:"
	campDetailsCallbackPrefix = "camp_details:"
	aboutCallbackData         = "about"
	askCallbackData           = "ask"
	operatorCallbackData      = "operator"
	menuCallbackData          = "menu"
	takeTicketPrefix          = "ticket:take:"
	closeTicketPrefix         = "ticket:close:"

	askButtonText         = "✍️ Задать вопрос"
	aboutButtonText       = "🎾 О Dzala"
	campInfoButtonText    = "🎾 О кэмпе"
	campDetailsButtonText = "📋 Подробнее"
	operatorButtonText    = "👤 Связаться с оператором"
	menuButtonText        = "⬅️ Главное меню"
	takeButtonText        = "✅ Взять заявку"
	closeButtonText       = "❌ Закрыть заявку"

	maxQuestionLength = 1000
)

// Client messages.
const (
	startMessage            = "Привет! Я помогу разобраться с теннисными кэмпами Dzala. Выберите направление или задайте вопрос:"
	menuMessage             = "Главное меню. Выберите направление или задайте вопрос:"
	aboutMessage            = "Раздел о Dzala сейчас готовится. Я могу помочь с информацией о кэмпах или связать вас с оператором."
	askPromptMessage        = "Напишите ваш вопрос. Можно спросить о программе, тренировках, проживании или стоимости кэмпа."
	campUnavailableMessage  = "Описание этого кэмпа сейчас недоступно. Могу передать ваш вопрос оператору."
	questionLengthMessage   = "Вопрос должен содержать от 1 до 1000 символов."
	aiFailureMessage        = "Не получилось подготовить ответ. Могу передать ваш вопрос оператору."
	handoffDoneMessage      = "Передал ваш вопрос оператору. Ответ придёт сюда, в этот чат."
	operatorPromptMessage   = "Напишите вопрос, который нужно передать оператору."
	operatorBusyMessage     = "Ваш вопрос уже у оператора. Напишите сообщение — я передам его."
	operatorsDownMessage    = "Сейчас не удалось подключить оператора. Попробуйте немного позже."
	relayFailedMessage      = "Не получилось передать сообщение оператору. Попробуйте ещё раз немного позже."
	ticketClosedMessage     = "Диалог с оператором завершён. Если появится новый вопрос, просто напишите его."
	textOnlyMessage         = "Пока я понимаю только текстовые сообщения. Опишите, пожалуйста, вопрос словами."
	textOnlyOperatorMessage = "Пока поддерживается только текст, поэтому я не смог передать это оператору."
	aiPromptMessage         = "Можете задать вопрос прямо здесь — наш ИИ попробует ответить по информации о кэмпах."
	sessionClosedMessage    = "Сессия завершена. Чтобы начать новую, отправьте любое сообщение или команду /start."
	clientExitedNotice      = "Клиент завершил сессию командой /exit."
	clientRestartedNotice   = "Клиент начал новую сессию командой /start."
)

// Operator group messages.
const (
	ticketTakenNotice     = "Эту заявку уже взял другой оператор"
	ticketOwnNotice       = "Вы уже взяли эту заявку"
	ticketClosedNotice    = "Заявка уже закрыта"
	ticketUnknownNotice   = "Заявка не найдена. Возможно, бот перезапускался"
	ticketNotTakenNotice  = "Сначала нажмите «Взять заявку»"
	operatorTextOnlyReply = "Пока поддерживается только текст, клиенту это не отправлено."
)

type app struct {
	store         *store
	knowledge     string
	ai            *aiClient
	managerChatID int64
}

func (a *app) operatorsEnabled() bool {
	return a.managerChatID != 0
}

func (a *app) ensureSession(chatID int64) {
	if !a.store.sessionActive(chatID) {
		a.store.startSession(chatID)
	}
}

func (a *app) startHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	if update == nil || update.Message == nil {
		return
	}
	if a.operatorsEnabled() && update.Message.Chat.ID == a.managerChatID {
		return
	}

	a.finishSession(ctx, telegramBot, update.Message.Chat.ID, clientRestartedNotice)
	a.store.startSession(update.Message.Chat.ID)
	sendMessage(ctx, telegramBot, &bot.SendMessageParams{
		ChatID:      update.Message.Chat.ID,
		Text:        startMessage,
		ReplyMarkup: mainMenuKeyboard(),
	})
}

func (a *app) exitHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	if update == nil || update.Message == nil {
		return
	}
	if a.operatorsEnabled() && update.Message.Chat.ID == a.managerChatID {
		return
	}

	a.finishSession(ctx, telegramBot, update.Message.Chat.ID, clientExitedNotice)
	sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: update.Message.Chat.ID, Text: sessionClosedMessage})
}

func (a *app) finishSession(ctx context.Context, telegramBot *bot.Bot, chatID int64, operatorNotice string) {
	current, hadTicket := a.store.endSession(chatID)
	if !hadTicket {
		return
	}
	a.updateCard(ctx, telegramBot, current, nil)
	if current.cardMessageID != 0 {
		sendMessage(ctx, telegramBot, &bot.SendMessageParams{
			ChatID:          a.managerChatID,
			Text:            operatorNotice,
			ReplyParameters: &models.ReplyParameters{MessageID: current.cardMessageID, AllowSendingWithoutReply: true},
		})
	}
}

func (a *app) chatIDHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	if update == nil || update.Message == nil || update.Message.Chat.ID >= 0 {
		return
	}

	sendMessage(ctx, telegramBot, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   fmt.Sprintf("ID этой группы: %d", update.Message.Chat.ID),
	})
}

func (a *app) menuHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	chatID, ok := clientCallbackChat(ctx, telegramBot, update)
	if !ok {
		return
	}

	a.ensureSession(chatID)
	a.store.update(chatID, func(state *chatState) { state.stage = stageIdle })
	sendMessage(ctx, telegramBot, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        menuMessage,
		ReplyMarkup: mainMenuKeyboard(),
	})
}

func (a *app) campHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	chatID, ok := clientCallbackChat(ctx, telegramBot, update)
	if !ok {
		return
	}

	campID := strings.TrimPrefix(update.CallbackQuery.Data, campCallbackPrefix)
	selected, ok := campByID(campID)
	if !ok {
		sendMessage(ctx, telegramBot, &bot.SendMessageParams{
			ChatID:      chatID,
			Text:        menuMessage,
			ReplyMarkup: mainMenuKeyboard(),
		})
		return
	}

	a.ensureSession(chatID)
	a.store.selectCamp(chatID, selected.id)
	title := campTitle(a.knowledge, selected.id)
	sendMessage(ctx, telegramBot, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        fmt.Sprintf("Вы выбрали «%s».\n\n%s", title, aiPromptMessage),
		ReplyMarkup: selectedCampKeyboard(selected.id),
	})
}

func (a *app) campInfoHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	chatID, ok := clientCallbackChat(ctx, telegramBot, update)
	if !ok {
		return
	}
	campID := strings.TrimPrefix(update.CallbackQuery.Data, campInfoCallbackPrefix)
	selected, ok := campByID(campID)
	if !ok {
		return
	}

	a.ensureSession(chatID)
	a.store.selectCamp(chatID, campID)
	summary, ok := campSummary(a.knowledge, selected)
	if !ok {
		summary = campUnavailableMessage
	}
	sendMessage(ctx, telegramBot, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        summary + "\n\n" + aiPromptMessage,
		ReplyMarkup: campInfoKeyboard(campID),
	})
}

func (a *app) campDetailsHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	chatID, ok := clientCallbackChat(ctx, telegramBot, update)
	if !ok {
		return
	}
	campID := strings.TrimPrefix(update.CallbackQuery.Data, campDetailsCallbackPrefix)
	selected, ok := campByID(campID)
	if !ok {
		return
	}

	a.ensureSession(chatID)
	a.store.selectCamp(chatID, campID)
	details, ok := campDetails(a.knowledge, selected)
	if !ok {
		details = campUnavailableMessage
	}
	a.sendLongClientMessage(ctx, telegramBot, chatID, details+"\n\n"+aiPromptMessage, selectedCampKeyboard(campID))
}

func (a *app) aboutHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	chatID, ok := clientCallbackChat(ctx, telegramBot, update)
	if !ok {
		return
	}

	a.ensureSession(chatID)
	sendMessage(ctx, telegramBot, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        aboutMessage,
		ReplyMarkup: operatorAndMenuKeyboard(),
	})
}

func (a *app) askHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	chatID, ok := clientCallbackChat(ctx, telegramBot, update)
	if !ok {
		return
	}

	a.ensureSession(chatID)
	a.store.update(chatID, func(state *chatState) { state.stage = stageAwaitingQuestion })
	sendMessage(ctx, telegramBot, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        askPromptMessage + "\n\n" + aiPromptMessage,
		ReplyMarkup: operatorAndMenuKeyboard(),
	})
}

func (a *app) operatorHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	chatID, ok := clientCallbackChat(ctx, telegramBot, update)
	if !ok {
		return
	}

	a.ensureSession(chatID)
	if _, active := a.store.activeTicket(chatID); active {
		sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: chatID, Text: operatorBusyMessage})
		return
	}

	state := a.store.chat(chatID)
	if state.lastQuestion == "" {
		a.store.update(chatID, func(state *chatState) { state.stage = stageAwaitingOperatorQuestion })
		sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: chatID, Text: operatorPromptMessage, ReplyMarkup: operatorAndMenuKeyboard()})
		return
	}

	a.openTicket(ctx, telegramBot, chatID, &update.CallbackQuery.From, state.lastQuestion, state.lastAnswer)
}

// messageHandler is the default handler for every message that is not a command.
func (a *app) messageHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	if update == nil || update.Message == nil {
		return
	}

	message := update.Message
	if a.operatorsEnabled() && message.Chat.ID == a.managerChatID {
		a.operatorMessageHandler(ctx, telegramBot, message)
		return
	}

	text := strings.TrimSpace(message.Text)
	if text == "" {
		if !hasUnsupportedContent(message) {
			return
		}
		notice := textOnlyMessage
		if _, active := a.store.activeTicket(message.Chat.ID); active {
			notice = textOnlyOperatorMessage
		}
		sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: message.Chat.ID, Text: notice})
		return
	}

	if questionExceedsLimit(text) {
		sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: message.Chat.ID, Text: questionLengthMessage})
		return
	}

	if !a.store.sessionActive(message.Chat.ID) {
		a.store.startSession(message.Chat.ID)
		sendMessage(ctx, telegramBot, &bot.SendMessageParams{
			ChatID:      message.Chat.ID,
			Text:        startMessage,
			ReplyMarkup: mainMenuKeyboard(),
		})
	}

	if current, active := a.store.activeTicket(message.Chat.ID); active {
		a.store.appendHistory(message.Chat.ID, sessionRoleUser, text)
		a.relayToOperators(ctx, telegramBot, current, text)
		return
	}

	if a.store.chat(message.Chat.ID).stage == stageAwaitingOperatorQuestion {
		a.store.appendHistory(message.Chat.ID, sessionRoleUser, text)
		a.openTicket(ctx, telegramBot, message.Chat.ID, message.From, text, "")
		return
	}

	a.answerQuestion(ctx, telegramBot, message.Chat.ID, message.From, text)
}

// answerQuestion asks the AI or escalates to an operator.
func (a *app) answerQuestion(ctx context.Context, telegramBot *bot.Bot, chatID int64, from *models.User, question string) {
	state := a.store.chat(chatID)
	history := state.history
	a.store.appendHistory(chatID, sessionRoleUser, question)

	switch planQuestion(question, a.ai.enabled(), a.operatorsEnabled()) {
	case planAI:
	case planOperator:
		if !requiresOperator(question) && a.sendLocalFallback(ctx, telegramBot, chatID, question, state.campID) {
			return
		}
		a.openTicket(ctx, telegramBot, chatID, from, question, "")
		return
	case planUnavailable:
		if a.sendLocalFallback(ctx, telegramBot, chatID, question, state.campID) {
			return
		}
		a.store.appendHistory(chatID, sessionRoleAssistant, operatorsDownMessage)
		sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: chatID, Text: operatorsDownMessage})
		return
	}

	selectedCamp := campTitle(a.knowledge, state.campID)
	decision, err := a.ai.ask(ctx, a.knowledge, selectedCamp, history, question)
	if err != nil {
		log.Printf("ask OpenRouter: %v", err)
		if a.sendLocalFallback(ctx, telegramBot, chatID, question, state.campID) {
			return
		}
		params := aiFailureResponse(chatID, a.operatorsEnabled())
		a.store.update(chatID, func(state *chatState) {
			state.stage = stageIdle
			state.lastQuestion = question
			state.lastAnswer = ""
		})
		a.store.appendHistory(chatID, sessionRoleAssistant, params.Text)
		sendMessage(ctx, telegramBot, params)
		return
	}

	if decision.Action == actionHandoff {
		if a.sendLocalFallback(ctx, telegramBot, chatID, question, state.campID) {
			return
		}
		a.openTicket(ctx, telegramBot, chatID, from, question, "")
		return
	}

	a.store.update(chatID, func(state *chatState) {
		state.stage = stageIdle
		state.lastQuestion = question
		state.lastAnswer = decision.Message
	})
	a.store.appendHistory(chatID, sessionRoleAssistant, decision.Message)
	sendMessage(ctx, telegramBot, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        decision.Message,
		ReplyMarkup: operatorKeyboard(),
	})
}

// openTicket creates a ticket and publishes its card in the operator group.
func (a *app) openTicket(ctx context.Context, telegramBot *bot.Bot, chatID int64, from *models.User, question, aiAnswer string) {
	if !a.operatorsEnabled() {
		a.store.update(chatID, func(state *chatState) {
			state.stage = stageIdle
			state.lastQuestion = question
			state.lastAnswer = aiAnswer
		})
		a.store.appendHistory(chatID, sessionRoleAssistant, operatorsDownMessage)
		sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: chatID, Text: operatorsDownMessage})
		return
	}

	created, createdNew := a.store.createTicket(chatID, campTitle(a.knowledge, a.store.chat(chatID).campID), question, aiAnswer)
	if !createdNew {
		a.store.appendHistory(chatID, sessionRoleAssistant, operatorBusyMessage)
		sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: chatID, Text: operatorBusyMessage})
		return
	}
	card := ticketCard(created, from)

	sent, err := telegramBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      a.managerChatID,
		Text:        card,
		ReplyMarkup: ticketKeyboard(created.id),
	})
	if err != nil {
		logTelegramError("send ticket to operators", err)
		a.store.dropTicket(created.id)
		a.store.appendHistory(chatID, sessionRoleAssistant, operatorsDownMessage)
		sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: chatID, Text: operatorsDownMessage})
		return
	}

	a.store.registerCard(created.id, sent.ID, card)
	a.sendTicketHistory(ctx, telegramBot, created, sent.ID)
	a.store.appendHistory(chatID, sessionRoleAssistant, handoffDoneMessage)
	sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: chatID, Text: handoffDoneMessage})
}

// relayToOperators forwards a client message into the ticket thread.
func (a *app) relayToOperators(ctx context.Context, telegramBot *bot.Bot, current ticket, text string) {
	sent, err := telegramBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: a.managerChatID,
		Text:   fmt.Sprintf("Заявка №%d, сообщение клиента:\n\n%s", current.id, text),
		ReplyParameters: &models.ReplyParameters{
			MessageID:                current.cardMessageID,
			AllowSendingWithoutReply: true,
		},
	})
	if err != nil {
		logTelegramError("relay client message", err)
		sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: current.clientChatID, Text: relayFailedMessage})
		return
	}
	a.store.linkGroupMessage(current.id, sent.ID)
}

// operatorMessageHandler relays operator replies back to the client.
func (a *app) operatorMessageHandler(ctx context.Context, telegramBot *bot.Bot, message *models.Message) {
	if message.ReplyToMessage == nil || message.From == nil {
		return
	}

	current, result := a.store.operatorReplyTarget(message.ReplyToMessage.ID, message.From.ID)
	notice := ""
	switch result {
	case replyAllowed:
	case replyUnknown:
		return
	case replyClosed:
		notice = ticketClosedNotice
	case replyNotTaken:
		notice = ticketNotTakenNotice
	case replyNotOwner:
		notice = ticketTakenNotice
	}
	if notice != "" {
		sendMessage(ctx, telegramBot, &bot.SendMessageParams{
			ChatID:          a.managerChatID,
			Text:            notice,
			ReplyParameters: &models.ReplyParameters{MessageID: message.ID, AllowSendingWithoutReply: true},
		})
		return
	}

	if strings.TrimSpace(message.Text) == "" {
		sendMessage(ctx, telegramBot, &bot.SendMessageParams{
			ChatID:          a.managerChatID,
			Text:            operatorTextOnlyReply,
			ReplyParameters: &models.ReplyParameters{MessageID: message.ID, AllowSendingWithoutReply: true},
		})
		return
	}

	sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: current.clientChatID, Text: message.Text})
	a.store.appendHistory(current.clientChatID, sessionRoleOperator, message.Text)
}

func (a *app) takeTicketHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	callback, ticketID, ok := a.resolveTicketCallback(ctx, telegramBot, update, takeTicketPrefix)
	if !ok {
		return
	}

	current, result := a.store.takeTicket(ticketID, callback.From.ID, operatorName(callback.From))
	switch result {
	case takeAssigned:
		answerCallback(ctx, telegramBot, callback.ID, "")
		a.updateCard(ctx, telegramBot, current, closeKeyboard(current.id))
	case takeAlreadyOwned:
		answerCallback(ctx, telegramBot, callback.ID, ticketOwnNotice)
	case takeTakenByOther:
		answerCallback(ctx, telegramBot, callback.ID, ticketTakenNotice)
	case takeClosed:
		answerCallback(ctx, telegramBot, callback.ID, ticketClosedNotice)
	default:
		answerCallback(ctx, telegramBot, callback.ID, ticketUnknownNotice)
	}
}

func (a *app) closeTicketHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	callback, ticketID, ok := a.resolveTicketCallback(ctx, telegramBot, update, closeTicketPrefix)
	if !ok {
		return
	}

	current, result := a.store.closeTicket(ticketID, callback.From.ID)
	switch result {
	case closeDone:
		answerCallback(ctx, telegramBot, callback.ID, "")
		a.updateCard(ctx, telegramBot, current, nil)
		a.store.appendHistory(current.clientChatID, sessionRoleAssistant, ticketClosedMessage)
		sendMessage(ctx, telegramBot, &bot.SendMessageParams{
			ChatID:      current.clientChatID,
			Text:        ticketClosedMessage,
			ReplyMarkup: mainMenuKeyboard(),
		})
	case closeForbidden:
		answerCallback(ctx, telegramBot, callback.ID, ticketTakenNotice)
	case closeAlreadyClosed:
		answerCallback(ctx, telegramBot, callback.ID, ticketClosedNotice)
	default:
		answerCallback(ctx, telegramBot, callback.ID, ticketUnknownNotice)
	}
}

// resolveTicketCallback validates that a ticket button was pressed inside the operator group.
func (a *app) resolveTicketCallback(ctx context.Context, telegramBot *bot.Bot, update *models.Update, prefix string) (*models.CallbackQuery, int64, bool) {
	if update == nil || update.CallbackQuery == nil {
		return nil, 0, false
	}

	callback := update.CallbackQuery
	if !a.operatorsEnabled() || callback.Message.Message == nil || callback.Message.Message.Chat.ID != a.managerChatID {
		answerCallback(ctx, telegramBot, callback.ID, ticketUnknownNotice)
		return nil, 0, false
	}

	ticketID, err := strconv.ParseInt(strings.TrimPrefix(callback.Data, prefix), 10, 64)
	if err != nil {
		answerCallback(ctx, telegramBot, callback.ID, ticketUnknownNotice)
		return nil, 0, false
	}
	return callback, ticketID, true
}

// updateCard appends a status line to the ticket card in the operator group.
func (a *app) updateCard(ctx context.Context, telegramBot *bot.Bot, current ticket, keyboard models.ReplyMarkup) {
	if current.cardMessageID == 0 {
		return
	}

	if _, err := telegramBot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:      a.managerChatID,
		MessageID:   current.cardMessageID,
		Text:        ticketCardWithStatus(current),
		ReplyMarkup: keyboard,
	}); err != nil {
		logTelegramError("update ticket card", err)
	}
}

type localFallbackHint struct {
	campID string
	topic  string
}

func matchLocalFallback(text, selectedCampID string) (localFallbackHint, bool) {
	text = strings.ToLower(text)
	hint := localFallbackHint{campID: selectedCampID}
	campMentioned := false
	for _, candidate := range []struct {
		campID  string
		aliases []string
	}{
		{campID: "tsinandali", aliases: []string{"цинандал", "ценандал", "tsinandali"}},
		{campID: "tbilisi", aliases: []string{"тбилис", "tbilisi"}},
		{campID: "cape_town", aliases: []string{"кейптаун", "кейп таун", "cape town", "capetown"}},
	} {
		for _, alias := range candidate.aliases {
			if strings.Contains(text, alias) {
				hint.campID = candidate.campID
				campMentioned = true
				break
			}
		}
		if campMentioned {
			break
		}
	}

	for _, candidate := range []struct {
		topic   string
		aliases []string
	}{
		{topic: "программа", aliases: []string{"программ", "расписан"}},
		{topic: "тренировки", aliases: []string{"тренир", "теннис"}},
		{topic: "проживание", aliases: []string{"прожив", "отел"}},
		{topic: "даты", aliases: []string{"когда"}},
		{topic: "стоимость", aliases: []string{"стоим", "ценник"}},
	} {
		for _, alias := range candidate.aliases {
			if strings.Contains(text, alias) {
				hint.topic = candidate.topic
				break
			}
		}
		if hint.topic != "" {
			break
		}
	}
	if hint.topic == "" && containsWord(text, "дата", "даты", "дату", "дате", "датой", "датам", "датах", "датами") {
		hint.topic = "даты"
	}
	if hint.topic == "" && containsWord(text, "цена", "цены", "цену", "цене", "ценой") {
		hint.topic = "стоимость"
	}
	return hint, campMentioned || hint.topic != ""
}

func containsWord(text string, variants ...string) bool {
	words := strings.FieldsFunc(text, func(char rune) bool {
		return !unicode.IsLetter(char) && !unicode.IsDigit(char)
	})
	for _, word := range words {
		for _, variant := range variants {
			if word == variant {
				return true
			}
		}
	}
	return false
}

func (a *app) sendLocalFallback(ctx context.Context, telegramBot *bot.Bot, chatID int64, question, selectedCampID string) bool {
	hint, ok := matchLocalFallback(question, selectedCampID)
	if !ok {
		return false
	}

	text := "Похоже, вас интересует информация о кэмпах. Выберите кэмп в главном меню."
	var keyboard models.ReplyMarkup = mainMenuKeyboard()
	if hint.campID != "" {
		title := campTitle(a.knowledge, hint.campID)
		text = fmt.Sprintf("Если вы имели в виду кэмп «%s» или хотите получить информацию о нём, нажмите «О кэмпе».", title)
		if hint.topic != "" {
			text = fmt.Sprintf("Похоже, вас интересует тема «%s» кэмпа «%s». Нажмите «О кэмпе», чтобы посмотреть проверенную информацию.", hint.topic, title)
		}
		keyboard = selectedCampKeyboard(hint.campID)
	}

	a.store.update(chatID, func(state *chatState) {
		state.stage = stageIdle
		state.lastQuestion = question
		state.lastAnswer = text
	})
	a.store.appendHistory(chatID, sessionRoleAssistant, text)
	sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: chatID, Text: text, ReplyMarkup: keyboard})
	return true
}

func formatSessionHistory(history []sessionMessage) string {
	lines := make([]string, 0, len(history))
	for _, entry := range history {
		label := "Бот"
		switch entry.role {
		case sessionRoleUser:
			label = "Клиент"
		case sessionRoleOperator:
			label = "Оператор"
		case sessionRoleAssistant:
		}
		lines = append(lines, label+": "+entry.text)
	}
	return strings.Join(lines, "\n\n")
}

func splitLongText(text string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	runes := []rune(strings.TrimSpace(text))
	parts := make([]string, 0, len(runes)/limit+1)
	for len(runes) > limit {
		cut := limit
		for probe := limit; probe > limit/2; probe-- {
			if runes[probe] == '\n' {
				cut = probe
				break
			}
		}
		parts = append(parts, strings.TrimSpace(string(runes[:cut])))
		runes = runes[cut:]
	}
	if tail := strings.TrimSpace(string(runes)); tail != "" {
		parts = append(parts, tail)
	}
	return parts
}

func (a *app) sendLongClientMessage(ctx context.Context, telegramBot *bot.Bot, chatID int64, text string, keyboard models.ReplyMarkup) {
	parts := splitLongText(text, 3500)
	for index, part := range parts {
		params := &bot.SendMessageParams{ChatID: chatID, Text: part}
		if index == len(parts)-1 {
			params.ReplyMarkup = keyboard
		}
		sendMessage(ctx, telegramBot, params)
	}
}

func (a *app) sendTicketHistory(ctx context.Context, telegramBot *bot.Bot, current ticket, cardMessageID int) {
	transcript := formatSessionHistory(current.history)
	if transcript == "" {
		return
	}
	for _, part := range splitLongText("История сессии:\n\n"+transcript, 3500) {
		sent, err := telegramBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          a.managerChatID,
			Text:            part,
			ReplyParameters: &models.ReplyParameters{MessageID: cardMessageID, AllowSendingWithoutReply: true},
		})
		if err != nil {
			logTelegramError("send session history", err)
			return
		}
		a.store.linkGroupMessage(current.id, sent.ID)
	}
}

type questionPlan int

const (
	planAI questionPlan = iota
	planOperator
	planUnavailable
)

func hasUnsupportedContent(message *models.Message) bool {
	return message.Animation != nil ||
		message.Audio != nil ||
		message.Document != nil ||
		message.PaidMedia != nil ||
		len(message.Photo) > 0 ||
		message.Sticker != nil ||
		message.Story != nil ||
		message.Video != nil ||
		message.VideoNote != nil ||
		message.Voice != nil ||
		message.Checklist != nil ||
		message.Contact != nil ||
		message.Dice != nil ||
		message.Game != nil ||
		message.Poll != nil ||
		message.Venue != nil ||
		message.Location != nil ||
		message.UsersShared != nil ||
		message.ChatShared != nil ||
		message.WebAppData != nil ||
		message.LivePhoto != nil
}

func questionExceedsLimit(question string) bool {
	return utf8.RuneCountInString(question) > maxQuestionLength
}

func aiFailureResponse(chatID int64, operatorsEnabled bool) *bot.SendMessageParams {
	if !operatorsEnabled {
		return &bot.SendMessageParams{ChatID: chatID, Text: operatorsDownMessage}
	}
	return &bot.SendMessageParams{ChatID: chatID, Text: aiFailureMessage, ReplyMarkup: operatorKeyboard()}
}

// planQuestion decides how a question is handled before any AI call is made.
func planQuestion(question string, aiEnabled, operatorsEnabled bool) questionPlan {
	if !requiresOperator(question) && aiEnabled {
		return planAI
	}
	if operatorsEnabled {
		return planOperator
	}
	return planUnavailable
}

// requiresOperator covers topics the knowledge base is not allowed to answer.
func requiresOperator(question string) bool {
	question = strings.ToLower(question)
	for _, trigger := range []string{
		"брониров", "заброни", "оплат", "платеж", "платёж", "депозит", "скидк", "рассрочк",
		"есть ли места", "свободные места", "свободно мест", "наличие мест", "остались места",
		"возврат", "отмен", "виз", "перелёт", "перелет", "авиабилет", "билет",
		"медицин", "травм", "аллерг", "страхов", "нестандартн",
		"оператор", "поговорить с человеком", "менеджер",
	} {
		if strings.Contains(question, trigger) {
			return true
		}
	}
	return strings.Contains(question, "индивидуальн") &&
		(strings.Contains(question, "услов") || strings.Contains(question, "размещ"))
}

func ticketCard(current ticket, from *models.User) string {
	selectedCamp := current.campTitle
	if selectedCamp == "" {
		selectedCamp = "не выбран"
	}

	lines := []string{
		"Новая заявка",
		"",
		fmt.Sprintf("Заявка №%d", current.id),
		"Кэмп: " + selectedCamp,
	}
	if from != nil {
		if name := strings.TrimSpace(from.FirstName + " " + from.LastName); name != "" {
			lines = append(lines, "Имя: "+name)
		}
		if from.Username != "" {
			lines = append(lines, "Telegram: @"+from.Username)
		}
	}
	lines = append(lines, "", "Вопрос: "+current.question)
	if current.aiAnswer != "" {
		lines = append(lines, "", "Ответ ИИ: "+current.aiAnswer)
	}
	return strings.Join(lines, "\n")
}

func ticketCardWithStatus(current ticket) string {
	text := current.card
	if current.operatorID != 0 {
		text += "\n\nЗаявку взял: " + current.operatorName
	}
	if current.status == ticketClosed {
		text += "\n\nЗаявка закрыта."
	}
	return text
}

func operatorName(from models.User) string {
	if name := strings.TrimSpace(from.FirstName + " " + from.LastName); name != "" {
		return name
	}
	if from.Username != "" {
		return "@" + from.Username
	}
	return "оператор"
}

// clientCallbackChat answers a client callback query and returns its chat.
func clientCallbackChat(ctx context.Context, telegramBot *bot.Bot, update *models.Update) (int64, bool) {
	if update == nil || update.CallbackQuery == nil {
		return 0, false
	}

	answerCallback(ctx, telegramBot, update.CallbackQuery.ID, "")
	if update.CallbackQuery.Message.Message == nil {
		return 0, false
	}
	return update.CallbackQuery.Message.Message.Chat.ID, true
}

func telegramErrorSummary(err error) string {
	switch {
	case errors.Is(err, bot.ErrorForbidden):
		return "forbidden"
	case errors.Is(err, bot.ErrorBadRequest):
		return "bad request"
	case errors.Is(err, bot.ErrorUnauthorized):
		return "unauthorized"
	case errors.Is(err, bot.ErrorNotFound):
		return "not found"
	case errors.Is(err, bot.ErrorConflict):
		return "conflict"
	case bot.IsTooManyRequestsError(err):
		return "rate limited"
	case bot.IsMigrateError(err):
		return "chat migrated"
	case errors.Is(err, context.Canceled):
		return "request canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "request timed out"
	default:
		return "request failed"
	}
}

func logTelegramError(operation string, err error) {
	log.Printf("%s: %s", operation, telegramErrorSummary(err))
}

func answerCallback(ctx context.Context, telegramBot *bot.Bot, callbackID, text string) {
	if _, err := telegramBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: callbackID,
		Text:            text,
		ShowAlert:       text != "",
	}); err != nil {
		logTelegramError("answer callback query", err)
	}
}

func sendMessage(ctx context.Context, telegramBot *bot.Bot, params *bot.SendMessageParams) {
	if _, err := telegramBot.SendMessage(ctx, params); err != nil {
		logTelegramError("send Telegram message", err)
	}
}

func mainMenuKeyboard() *models.InlineKeyboardMarkup {
	rows := make([][]models.InlineKeyboardButton, 0, 3)
	row := make([]models.InlineKeyboardButton, 0, 2)
	for _, item := range camps {
		row = append(row, models.InlineKeyboardButton{Text: item.label, CallbackData: campCallbackPrefix + item.id})
		if len(row) == 2 {
			rows = append(rows, row)
			row = make([]models.InlineKeyboardButton, 0, 2)
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}

	return &models.InlineKeyboardMarkup{InlineKeyboard: append(rows, []models.InlineKeyboardButton{
		{Text: aboutButtonText, CallbackData: aboutCallbackData},
		{Text: askButtonText, CallbackData: askCallbackData},
	})}
}

func selectedCampKeyboard(campID string) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: campInfoButtonText, CallbackData: campInfoCallbackPrefix + campID}},
		{{Text: operatorButtonText, CallbackData: operatorCallbackData}},
		{{Text: menuButtonText, CallbackData: menuCallbackData}},
	}}
}

func campInfoKeyboard(campID string) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: campDetailsButtonText, CallbackData: campDetailsCallbackPrefix + campID}},
		{{Text: operatorButtonText, CallbackData: operatorCallbackData}},
		{{Text: menuButtonText, CallbackData: menuCallbackData}},
	}}
}

func operatorKeyboard() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: operatorButtonText, CallbackData: operatorCallbackData}},
		{{Text: menuButtonText, CallbackData: menuCallbackData}},
	}}
}

func operatorAndMenuKeyboard() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: operatorButtonText, CallbackData: operatorCallbackData}},
		{{Text: menuButtonText, CallbackData: menuCallbackData}},
	}}
}

func ticketKeyboard(ticketID int64) *models.InlineKeyboardMarkup {
	id := strconv.FormatInt(ticketID, 10)
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{
			{Text: takeButtonText, CallbackData: takeTicketPrefix + id},
			{Text: closeButtonText, CallbackData: closeTicketPrefix + id},
		},
	}}
}

func closeKeyboard(ticketID int64) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: closeButtonText, CallbackData: closeTicketPrefix + strconv.FormatInt(ticketID, 10)}},
	}}
}
