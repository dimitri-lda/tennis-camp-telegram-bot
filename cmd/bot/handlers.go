package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const (
	campCallbackPrefix   = "camp:"
	aboutCallbackData    = "about"
	askCallbackData      = "ask"
	operatorCallbackData = "operator"
	menuCallbackData     = "menu"
	takeTicketPrefix     = "ticket:take:"
	closeTicketPrefix    = "ticket:close:"

	askButtonText      = "✍️ Задать вопрос"
	aboutButtonText    = "🎾 О Dzala"
	operatorButtonText = "👤 Связаться с оператором"
	menuButtonText     = "⬅️ Главное меню"
	takeButtonText     = "✅ Взять заявку"
	closeButtonText    = "❌ Закрыть заявку"

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

func (a *app) startHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	if update == nil || update.Message == nil {
		return
	}
	if a.operatorsEnabled() && update.Message.Chat.ID == a.managerChatID {
		return
	}

	a.store.update(update.Message.Chat.ID, func(state *chatState) { state.stage = stageIdle })
	sendMessage(ctx, telegramBot, &bot.SendMessageParams{
		ChatID:      update.Message.Chat.ID,
		Text:        startMessage,
		ReplyMarkup: mainMenuKeyboard(),
	})
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

	a.store.selectCamp(chatID, selected.id)
	summary, ok := campSummary(a.knowledge, selected)
	if !ok {
		summary = campUnavailableMessage
	}

	sendMessage(ctx, telegramBot, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        summary,
		ReplyMarkup: campKeyboard(),
	})
}

func (a *app) aboutHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	chatID, ok := clientCallbackChat(ctx, telegramBot, update)
	if !ok {
		return
	}

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

	a.store.update(chatID, func(state *chatState) { state.stage = stageAwaitingQuestion })
	sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: chatID, Text: askPromptMessage})
}

func (a *app) operatorHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	chatID, ok := clientCallbackChat(ctx, telegramBot, update)
	if !ok {
		return
	}

	if _, active := a.store.activeTicket(chatID); active {
		sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: chatID, Text: operatorBusyMessage})
		return
	}

	state := a.store.chat(chatID)
	if state.lastQuestion == "" {
		a.store.update(chatID, func(state *chatState) { state.stage = stageAwaitingOperatorQuestion })
		sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: chatID, Text: operatorPromptMessage})
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

	if current, active := a.store.activeTicket(message.Chat.ID); active {
		a.relayToOperators(ctx, telegramBot, current, text)
		return
	}

	if a.store.chat(message.Chat.ID).stage == stageAwaitingOperatorQuestion {
		a.openTicket(ctx, telegramBot, message.Chat.ID, message.From, text, "")
		return
	}

	a.answerQuestion(ctx, telegramBot, message.Chat.ID, message.From, text)
}

// answerQuestion asks the AI or escalates to an operator.
func (a *app) answerQuestion(ctx context.Context, telegramBot *bot.Bot, chatID int64, from *models.User, question string) {
	switch planQuestion(question, a.ai.enabled(), a.operatorsEnabled()) {
	case planAI:
	case planOperator:
		a.openTicket(ctx, telegramBot, chatID, from, question, "")
		return
	case planUnavailable:
		sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: chatID, Text: operatorsDownMessage})
		return
	}

	selectedCamp := campTitle(a.knowledge, a.store.chat(chatID).campID)
	decision, err := a.ai.ask(ctx, a.knowledge, selectedCamp, question)
	if err != nil {
		log.Printf("ask OpenRouter: %v", err)
		a.store.update(chatID, func(state *chatState) {
			state.stage = stageIdle
			state.lastQuestion = question
			state.lastAnswer = ""
		})
		sendMessage(ctx, telegramBot, aiFailureResponse(chatID, a.operatorsEnabled()))
		return
	}

	if decision.Action == actionHandoff {
		a.openTicket(ctx, telegramBot, chatID, from, question, "")
		return
	}

	a.store.update(chatID, func(state *chatState) {
		state.stage = stageIdle
		state.lastQuestion = question
		state.lastAnswer = decision.Message
	})
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
		sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: chatID, Text: operatorsDownMessage})
		return
	}

	created, createdNew := a.store.createTicket(chatID, campTitle(a.knowledge, a.store.chat(chatID).campID), question, aiAnswer)
	if !createdNew {
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
		sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: chatID, Text: operatorsDownMessage})
		return
	}

	a.store.registerCard(created.id, sent.ID, card)
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

type questionPlan int

const (
	planAI questionPlan = iota
	planOperator
	planUnavailable
)

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

func campKeyboard() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: askButtonText, CallbackData: askCallbackData}},
		{{Text: operatorButtonText, CallbackData: operatorCallbackData}},
		{{Text: menuButtonText, CallbackData: menuCallbackData}},
	}}
}

func operatorKeyboard() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: operatorButtonText, CallbackData: operatorCallbackData}},
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
