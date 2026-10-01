package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const (
	campCallbackPrefix          = "camp:"
	campInfoCallbackPrefix      = "camp_info:"
	campDetailsCallbackPrefix   = "camp_details:"
	campTopicCallbackPrefix     = "camp_topic:"
	trainingTopicCallbackPrefix = "training_topic:"
	campsCallbackData           = "camps"
	trainingCallbackData        = "training"
	aboutCallbackData           = "about"
	askCallbackData             = "ask"
	operatorCallbackData        = "operator"
	menuCallbackData            = "menu"
	takeTicketPrefix            = "ticket:take:"
	closeTicketPrefix           = "ticket:close:"

	campsButtonText          = "🏕 Кэмпы"
	trainingButtonText       = "🎾 Тренировки"
	aboutButtonText          = "ℹ️ О Dzala"
	operatorButtonText       = "👤 Связаться с оператором"
	backToCampsButtonText    = "⬅️ К списку кэмпов"
	backToTrainingButtonText = "⬅️ К тренировкам"
	menuButtonText           = "⬅️ Главное меню"
	takeButtonText           = "✅ Взять заявку"
	closeButtonText          = "❌ Закрыть заявку"

	maxQuestionLength = 1000
)

// Client messages.
const (
	aiPromptMessage          = "ИИ-помощник Dzala ответит на вопрос прямо в чате. Если потребуется уточнение, подключу оператора."
	startMessage             = "Привет! Я ИИ-помощник Dzala. Помогу узнать о тренировках в Тбилиси, паделе и теннисных кэмпах. Выберите раздел или напишите вопрос."
	menuMessage              = "Выберите раздел или напишите вопрос. ИИ-помощник Dzala ответит прямо в чате."
	campsMessage             = "Выберите кэмп — сразу покажу краткую информацию:"
	trainingUnavailable      = "Информация о тренировках сейчас недоступна. Могу связать вас с оператором."
	aboutUnavailable         = "Информация о Dzala сейчас недоступна. Могу связать вас с оператором."
	askPromptMessage         = "Напишите вопрос о Dzala, регулярных тренировках, паделе или теннисных кэмпах."
	topicUnavailableMessage  = "Информация по этой теме сейчас недоступна. Могу связать вас с оператором."
	campUnavailableMessage   = "Описание этого кэмпа сейчас недоступно. Могу передать ваш вопрос оператору."
	questionLengthMessage    = "Вопрос должен содержать от 1 до 1000 символов."
	handoffDoneMessage       = "Передал ваш вопрос оператору. Ответ придёт сюда, в этот чат."
	operatorPromptMessage    = "Напишите вопрос, который нужно передать оператору."
	operatorBusyMessage      = "Ваш вопрос уже у оператора. Напишите сообщение — я передам его."
	operatorsDownMessage     = "Сейчас не удалось подключить оператора. Попробуйте немного позже."
	relayFailedMessage       = "Не получилось передать сообщение оператору. Попробуйте ещё раз немного позже."
	ticketClosedMessage      = "Диалог с оператором завершён. Если появится новый вопрос, просто напишите его."
	textOnlyMessage          = "Пока я понимаю только текстовые сообщения. Опишите, пожалуйста, вопрос словами."
	textOnlyOperatorMessage  = "Пока поддерживается только текст, поэтому я не смог передать это оператору."
	sessionClosedMessage     = "Сессия завершена. Чтобы начать новую, отправьте любое сообщение или команду /start."
	clientExitedNotice       = "Клиент завершил сессию командой /exit."
	clientRestartedNotice    = "Клиент начал новую сессию командой /start."
	helpMessage              = "Как пользоваться ботом:\n\n• /start — начать новую сессию\n• выберите «Тренировки», «Кэмпы» или «О Dzala»\n• напишите вопрос прямо в чат\n• «Связаться с оператором» — передать диалог человеку\n• /exit — завершить сессию"
	aiBusyMessage            = "Я ещё готовлю ответ на предыдущий вопрос. Подождите немного."
	aiTooSoonMessage         = "Подождите несколько секунд перед следующим вопросом."
	aiQuestionLimitMessage   = "Лимит вопросов к ИИ в этой сессии исчерпан. Могу связать вас с оператором."
	idleSessionClosedMessage = "Сессия автоматически завершена из-за отсутствия активности. Чтобы начать новую, отправьте любое сообщение или /start."
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
	store                      *store
	knowledge                  string
	ai                         *aiClient
	managerChatID              int64
	aiSessionIdleTimeout       time.Duration
	operatorSessionIdleTimeout time.Duration
	sessionSweepInterval       time.Duration
	aiQuestionInterval         time.Duration
	aiMaxQuestions             int
	stats                      *botStats
	sessionLocks               sync.Map
}

func (a *app) sessionLock(chatID int64) *sync.Mutex {
	value, _ := a.sessionLocks.LoadOrStore(chatID, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func (a *app) operatorsEnabled() bool {
	return a.managerChatID != 0
}

func (a *app) ensureSession(chatID int64) {
	lock := a.sessionLock(chatID)
	lock.Lock()
	defer lock.Unlock()
	if !a.store.sessionActive(chatID) {
		a.store.startSession(chatID)
		if a.stats != nil {
			a.stats.sessionsStarted.Add(1)
		}
	}
}

func (a *app) aiLimits() (time.Duration, int) {
	interval := a.aiQuestionInterval
	if interval <= 0 {
		interval = defaultAIQuestionInterval
	}
	maximum := a.aiMaxQuestions
	if maximum <= 0 {
		maximum = defaultAIMaxQuestions
	}
	return interval, maximum
}

func (a *app) startHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	if update == nil || update.Message == nil {
		return
	}
	if a.operatorsEnabled() && update.Message.Chat.ID == a.managerChatID {
		return
	}

	lock := a.sessionLock(update.Message.Chat.ID)
	lock.Lock()
	defer lock.Unlock()
	a.finishSession(ctx, telegramBot, update.Message.Chat.ID, clientRestartedNotice)
	a.store.startSession(update.Message.Chat.ID)
	if a.stats != nil {
		a.stats.sessionsStarted.Add(1)
	}
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

	lock := a.sessionLock(update.Message.Chat.ID)
	lock.Lock()
	defer lock.Unlock()
	a.finishSession(ctx, telegramBot, update.Message.Chat.ID, clientExitedNotice)
	sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: update.Message.Chat.ID, Text: sessionClosedMessage})
}

func (a *app) finishSession(ctx context.Context, telegramBot *bot.Bot, chatID int64, operatorNotice string) {
	a.publishSessionClosure(ctx, telegramBot, a.store.closeSession(chatID), operatorNotice)
}

func (a *app) publishSessionClosure(ctx context.Context, telegramBot *bot.Bot, closure sessionClosure, operatorNotice string) {
	duration := closure.endedAt.Sub(closure.startedAt).Round(time.Minute)
	if duration < time.Minute {
		duration = time.Minute
	}
	operatorNotice += "\nПродолжительность: " + duration.String()
	messageID := closure.auditMessageID
	if closure.hadTicket {
		messageID = closure.ticket.cardMessageID
		a.updateCard(ctx, telegramBot, closure.ticket, nil)
	} else if messageID != 0 {
		card := strings.Replace(closure.auditCard, "Статус: отвечает ИИ", "Статус: завершена", 1)
		if _, err := telegramBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    a.managerChatID,
			MessageID: messageID,
			Text:      card,
		}); err != nil {
			logTelegramError("close AI session card", err)
		}
	}
	if messageID == 0 {
		return
	}
	sendMessage(ctx, telegramBot, &bot.SendMessageParams{
		ChatID:          a.managerChatID,
		Text:            operatorNotice,
		ReplyParameters: &models.ReplyParameters{MessageID: messageID, AllowSendingWithoutReply: true},
	})
	a.sendSessionTranscript(ctx, telegramBot, closure.history, messageID, "Итоговая история сессии")
}

func (a *app) sessionTimeouts() (time.Duration, time.Duration, time.Duration) {
	aiTimeout := a.aiSessionIdleTimeout
	if aiTimeout <= 0 {
		aiTimeout = defaultAISessionIdleTimeout
	}
	operatorTimeout := a.operatorSessionIdleTimeout
	if operatorTimeout <= 0 {
		operatorTimeout = defaultOperatorSessionIdleTimeout
	}
	sweep := a.sessionSweepInterval
	if sweep <= 0 {
		sweep = defaultSessionSweepInterval
	}
	return aiTimeout, operatorTimeout, sweep
}

func (a *app) runSessionSweeper(ctx context.Context, telegramBot *bot.Bot) {
	aiTimeout, operatorTimeout, sweep := a.sessionTimeouts()
	ticker := time.NewTicker(sweep)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			for _, chatID := range a.store.expiredChatIDs(now, aiTimeout, operatorTimeout) {
				lock := a.sessionLock(chatID)
				lock.Lock()
				state := a.store.chat(chatID)
				closure, ok := a.store.expireSessionIfIdle(chatID, state.sessionID, now, aiTimeout, operatorTimeout)
				if ok {
					if a.stats != nil {
						a.stats.timeouts.Add(1)
					}
					a.publishSessionClosure(ctx, telegramBot, closure, "Сессия завершена из-за отсутствия активности.")
					sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: chatID, Text: idleSessionClosedMessage})
				}
				lock.Unlock()
			}
		}
	}
}

func (a *app) helpHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	if update == nil || update.Message == nil {
		return
	}
	if a.operatorsEnabled() && update.Message.Chat.ID == a.managerChatID {
		return
	}
	sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: update.Message.Chat.ID, Text: helpMessage, ReplyMarkup: mainMenuKeyboard()})
}

func (a *app) statsHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	if update == nil || update.Message == nil || !a.operatorsEnabled() || update.Message.Chat.ID != a.managerChatID {
		return
	}
	var sessions, requests, successes, failures, handoffs, timeouts, busy, throttled uint64
	if a.stats != nil {
		sessions = a.stats.sessionsStarted.Load()
		requests = a.stats.aiRequests.Load()
		successes = a.stats.aiSuccess.Load()
		failures = a.stats.aiFailure.Load()
		handoffs = a.stats.handoffs.Load()
		timeouts = a.stats.timeouts.Load()
		busy = a.stats.busyRejected.Load()
		throttled = a.stats.throttled.Load()
	}
	text := fmt.Sprintf("Статистика после запуска\n\nСессии: %d\nAI-запросы: %d\nУспешные ответы: %d\nОшибки AI: %d\nПередачи оператору: %d\nАвтозакрытия: %d\nОтклонено занятых: %d\nОграничено по частоте: %d", sessions, requests, successes, failures, handoffs, timeouts, busy, throttled)
	sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: update.Message.Chat.ID, Text: text})
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
	a.store.selectSection(chatID, sectionGeneral)
	sendMessage(ctx, telegramBot, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        menuMessage,
		ReplyMarkup: mainMenuKeyboard(),
	})
}

func (a *app) campsHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	chatID, ok := clientCallbackChat(ctx, telegramBot, update)
	if !ok {
		return
	}

	a.ensureSession(chatID)
	a.store.selectSection(chatID, sectionCamps)
	sendMessage(ctx, telegramBot, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        campsMessage,
		ReplyMarkup: campsMenuKeyboard(),
	})
}

func (a *app) trainingHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	chatID, ok := clientCallbackChat(ctx, telegramBot, update)
	if !ok {
		return
	}

	a.ensureSession(chatID)
	a.store.selectSection(chatID, sectionTraining)
	text, ok := trainingInfo(a.knowledge)
	if !ok {
		text = trainingUnavailable
	}
	message := text + "\n\n" + aiPromptMessage
	a.store.appendHistory(chatID, sessionRoleAssistant, message)
	sendMessage(ctx, telegramBot, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        message,
		ReplyMarkup: trainingKeyboard(),
	})
}

func (a *app) trainingTopicHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	chatID, ok := clientCallbackChat(ctx, telegramBot, update)
	if !ok {
		return
	}

	topicID := strings.TrimPrefix(update.CallbackQuery.Data, trainingTopicCallbackPrefix)
	label, text, ok := trainingTopicInfo(a.knowledge, topicID)
	if !ok {
		text = topicUnavailableMessage
		label = "Тренировки"
	}
	a.ensureSession(chatID)
	a.store.selectTopic(chatID, sectionTraining, label, "")
	message := text + "\n\n" + aiPromptMessage
	a.store.appendHistory(chatID, sessionRoleAssistant, message)
	a.sendLongClientMessage(ctx, telegramBot, chatID, message, trainingTopicKeyboard())
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
	a.sendCampSummary(ctx, telegramBot, chatID, selected)
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
	a.sendCampSummary(ctx, telegramBot, chatID, selected)
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
	a.sendCampSummary(ctx, telegramBot, chatID, selected)
}

func (a *app) campTopicHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	chatID, ok := clientCallbackChat(ctx, telegramBot, update)
	if !ok {
		return
	}

	parts := strings.SplitN(strings.TrimPrefix(update.CallbackQuery.Data, campTopicCallbackPrefix), ":", 2)
	if len(parts) != 2 {
		return
	}
	selected, ok := campByID(parts[0])
	if !ok {
		return
	}
	label, text, ok := campTopicInfo(a.knowledge, selected, parts[1])
	if !ok {
		text = topicUnavailableMessage
		label = "Кэмп"
	}

	a.ensureSession(chatID)
	a.store.selectTopic(chatID, sectionCamps, label, selected.id)
	message := text + "\n\n" + aiPromptMessage
	a.store.appendHistory(chatID, sessionRoleAssistant, message)
	a.sendLongClientMessage(ctx, telegramBot, chatID, message, campTopicKeyboard(selected.id))
}

func (a *app) sendCampSummary(ctx context.Context, telegramBot *bot.Bot, chatID int64, selected camp) {
	summary, ok := campSummary(a.knowledge, selected)
	if !ok {
		summary = campUnavailableMessage
	}
	message := summary + "\n\nВыберите тему ниже или напишите вопрос. ИИ-помощник Dzala ответит прямо в чате."
	a.store.appendHistory(chatID, sessionRoleAssistant, message)
	sendMessage(ctx, telegramBot, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        message,
		ReplyMarkup: selectedCampKeyboard(selected.id),
	})
}

func (a *app) aboutHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	chatID, ok := clientCallbackChat(ctx, telegramBot, update)
	if !ok {
		return
	}

	a.ensureSession(chatID)
	a.store.selectSection(chatID, sectionAbout)
	text, ok := aboutInfo(a.knowledge)
	if !ok {
		text = aboutUnavailable
	}
	message := text + "\n\n" + aiPromptMessage
	a.store.appendHistory(chatID, sessionRoleAssistant, message)
	sendMessage(ctx, telegramBot, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        message,
		ReplyMarkup: operatorAndMenuKeyboard(),
	})
}

func (a *app) askHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	chatID, ok := clientCallbackChat(ctx, telegramBot, update)
	if !ok {
		return
	}

	a.ensureSession(chatID)
	a.store.selectSection(chatID, sectionGeneral)
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
	lock := a.sessionLock(chatID)
	lock.Lock()
	defer lock.Unlock()
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
		a.ensureSession(message.Chat.ID)
		sendMessage(ctx, telegramBot, &bot.SendMessageParams{
			ChatID:      message.Chat.ID,
			Text:        startMessage,
			ReplyMarkup: mainMenuKeyboard(),
		})
	}

	lock := a.sessionLock(message.Chat.ID)
	lock.Lock()
	if current, active := a.store.activeTicket(message.Chat.ID); active {
		a.store.appendHistory(message.Chat.ID, sessionRoleUser, text)
		a.relayToOperators(ctx, telegramBot, current, text)
		lock.Unlock()
		return
	}
	if a.store.chat(message.Chat.ID).stage == stageAwaitingOperatorQuestion {
		a.store.appendHistory(message.Chat.ID, sessionRoleUser, text)
		a.openTicket(ctx, telegramBot, message.Chat.ID, message.From, text, "")
		lock.Unlock()
		return
	}
	lock.Unlock()

	a.answerQuestion(ctx, telegramBot, message.Chat.ID, message.From, text)
}

// answerQuestion asks the AI or escalates to an operator.
func (a *app) answerQuestion(ctx context.Context, telegramBot *bot.Bot, chatID int64, from *models.User, question string) {
	state := a.store.chat(chatID)
	switch planQuestion(question, a.ai.enabled(), a.operatorsEnabled()) {
	case planAI:
	case planOperator:
		a.store.appendHistory(chatID, sessionRoleUser, question)
		if !a.ai.enabled() && a.sendLocalFallback(ctx, telegramBot, chatID, question, state.campID) {
			return
		}
		a.openTicket(ctx, telegramBot, chatID, from, question, "")
		return
	case planUnavailable:
		a.store.appendHistory(chatID, sessionRoleUser, question)
		if a.sendLocalFallback(ctx, telegramBot, chatID, question, state.campID) {
			return
		}
		a.store.appendHistory(chatID, sessionRoleAssistant, operatorsDownMessage)
		sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: chatID, Text: operatorsDownMessage})
		return
	}

	aiCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	interval, maximum := a.aiLimits()
	attempt, result := a.store.beginAI(chatID, time.Now(), interval, maximum, cancel)
	if result != aiStartOK {
		cancel()
		a.rejectAIQuestion(ctx, telegramBot, chatID, question, result)
		return
	}
	a.store.appendHistory(chatID, sessionRoleUser, question)
	if a.stats != nil {
		a.stats.aiRequests.Add(1)
	}
	a.ensureAISessionCard(ctx, telegramBot, chatID, from, question, attempt)

	selectedCamp := campTitle(a.knowledge, attempt.campID)
	stopTyping := startTyping(aiCtx, telegramBot, chatID)
	decision, err := a.ai.ask(aiCtx, a.knowledge, attempt.section, attempt.topic, selectedCamp, attempt.history, question)
	stopTyping()
	lock := a.sessionLock(chatID)
	lock.Lock()
	defer lock.Unlock()
	if !a.store.finishAI(chatID, attempt.sessionID) {
		return
	}
	if err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return
		}
		log.Printf("ask OpenRouter: %v", err)
		if a.stats != nil {
			a.stats.aiFailure.Add(1)
		}
		if a.operatorsEnabled() {
			a.openTicket(ctx, telegramBot, chatID, from, question, "")
			return
		}
		a.store.update(chatID, func(state *chatState) {
			state.stage = stageIdle
			state.lastQuestion = question
			state.lastAnswer = ""
		})
		a.store.appendHistory(chatID, sessionRoleAssistant, operatorsDownMessage)
		sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: chatID, Text: operatorsDownMessage})
		return
	}

	if decision.Action == actionHandoff {
		a.openTicket(ctx, telegramBot, chatID, from, question, "")
		return
	}

	if a.stats != nil {
		a.stats.aiSuccess.Add(1)
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
		ReplyMarkup: contextKeyboard(attempt),
	})
}

func (a *app) recordHandoff() {
	if a.stats != nil {
		a.stats.handoffs.Add(1)
	}
}

func (a *app) rejectAIQuestion(ctx context.Context, telegramBot *bot.Bot, chatID int64, question string, result aiStartResult) {
	text := aiBusyMessage
	switch result {
	case aiStartBusy:
		if a.stats != nil {
			a.stats.busyRejected.Add(1)
		}
	case aiStartTooSoon:
		text = aiTooSoonMessage
		if a.stats != nil {
			a.stats.throttled.Add(1)
		}
	case aiStartLimit:
		text = aiQuestionLimitMessage
		if a.stats != nil {
			a.stats.throttled.Add(1)
		}
	case aiStartNoSession:
		text = sessionClosedMessage
	case aiStartOK:
		return
	}
	if result != aiStartNoSession {
		a.store.appendHistory(chatID, sessionRoleUser, question)
		a.store.appendHistory(chatID, sessionRoleAssistant, text)
	}
	params := &bot.SendMessageParams{ChatID: chatID, Text: text}
	if result == aiStartLimit {
		params.ReplyMarkup = operatorAndMenuKeyboard()
	}
	sendMessage(ctx, telegramBot, params)
}

func (a *app) ensureAISessionCard(ctx context.Context, telegramBot *bot.Bot, chatID int64, from *models.User, question string, state chatState) {
	if !a.operatorsEnabled() || state.auditMessageID != 0 {
		return
	}
	section := state.section
	if section == "" {
		section = "Общий вопрос"
	}
	lines := []string{
		fmt.Sprintf("AI-сессия №%d", state.sessionID),
		"",
		"Раздел: " + section,
		"Начало: " + state.startedAt.Format("02.01.2006 15:04"),
	}
	if state.topic != "" {
		lines = append(lines, "Тема: "+state.topic)
	}
	if title := campTitle(a.knowledge, state.campID); title != "" {
		lines = append(lines, "Кэмп: "+title)
	}
	if from != nil {
		if name := strings.TrimSpace(from.FirstName + " " + from.LastName); name != "" {
			lines = append(lines, "Имя: "+name)
		}
		if from.Username != "" {
			lines = append(lines, "Telegram: @"+from.Username)
		}
	}
	lines = append(lines, "", "Первый вопрос: "+question, "", "Статус: отвечает ИИ")
	card := strings.Join(lines, "\n")
	sent, err := telegramBot.SendMessage(ctx, &bot.SendMessageParams{ChatID: a.managerChatID, Text: card})
	if err != nil {
		logTelegramError("send AI session card", err)
		return
	}
	if !a.store.registerAuditCard(chatID, state.sessionID, sent.ID, card) {
		closedCard := strings.Replace(card, "Статус: отвечает ИИ", "Статус: завершена до ответа", 1)
		if _, err := telegramBot.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: a.managerChatID, MessageID: sent.ID, Text: closedCard}); err != nil {
			logTelegramError("close stale AI session card", err)
		}
	}
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
	a.recordHandoff()
	card := ticketCard(created, from)
	cardMessageID := created.cardMessageID
	if cardMessageID != 0 {
		if _, err := telegramBot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:      a.managerChatID,
			MessageID:   cardMessageID,
			Text:        card,
			ReplyMarkup: ticketKeyboard(created.id),
		}); err != nil {
			logTelegramError("convert AI session to ticket", err)
			cardMessageID = 0
		}
	}
	if cardMessageID == 0 {
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
		cardMessageID = sent.ID
	}

	a.store.registerCard(created.id, cardMessageID, card)
	a.sendTicketHistory(ctx, telegramBot, created, cardMessageID)
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
	if current.clientChatID != 0 {
		lock := a.sessionLock(current.clientChatID)
		lock.Lock()
		defer lock.Unlock()
		current, result = a.store.operatorReplyTarget(message.ReplyToMessage.ID, message.From.ID)
	}
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
		a.store.touch(current.clientChatID)
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
	target, ok := a.store.ticketByID(ticketID)
	if !ok {
		answerCallback(ctx, telegramBot, callback.ID, ticketUnknownNotice)
		return
	}
	lock := a.sessionLock(target.clientChatID)
	lock.Lock()
	defer lock.Unlock()

	current, result := a.store.closeTicket(ticketID, callback.From.ID)
	switch result {
	case closeDone:
		answerCallback(ctx, telegramBot, callback.ID, "")
		a.store.appendHistory(current.clientChatID, sessionRoleAssistant, ticketClosedMessage)
		closure := a.store.closeSession(current.clientChatID)
		current.history = closure.history
		a.updateCard(ctx, telegramBot, current, nil)
		a.sendSessionTranscript(ctx, telegramBot, closure.history, current.cardMessageID, "Итоговая история сессии")
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

// localFallbackReply builds a knowledge-based hint when the AI cannot answer.
// Topics that only an operator may answer are never replaced by a camp card.
func localFallbackReply(knowledge, question, selectedCampID string) (string, *models.InlineKeyboardMarkup, bool) {
	if requiresOperator(question) {
		return "", nil, false
	}

	hint, matched := matchLocalFallback(question, selectedCampID)
	if !matched && hint.campID == "" {
		return "", nil, false
	}
	if hint.campID == "" {
		if hint.topic == "тренировки" {
			return "Если вы имеете в виду регулярные тренировки в Тбилиси, нажмите «Тренировки». Если вас интересует программа кэмпа, выберите «Кэмпы».", mainMenuKeyboard(), true
		}
		return "Похоже, вас интересует информация о кэмпах. Выберите «Кэмпы» в главном меню.", mainMenuKeyboard(), true
	}

	title := campTitle(knowledge, hint.campID)
	text := fmt.Sprintf("Если вы имели в виду кэмп «%s», выберите нужную тему ниже.", title)
	if hint.topic != "" {
		text = fmt.Sprintf("Похоже, вас интересует тема «%s» кэмпа «%s». Выберите подходящий раздел ниже.", hint.topic, title)
	}
	return text, selectedCampKeyboard(hint.campID), true
}

func (a *app) sendLocalFallback(ctx context.Context, telegramBot *bot.Bot, chatID int64, question, selectedCampID string) bool {
	text, keyboard, ok := localFallbackReply(a.knowledge, question, selectedCampID)
	if !ok {
		return false
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
		label := "ИИ-помощник"
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

func (a *app) sendSessionTranscript(ctx context.Context, telegramBot *bot.Bot, history []sessionMessage, messageID int, heading string) {
	transcript := formatSessionHistory(history)
	if transcript == "" || messageID == 0 {
		return
	}
	for _, part := range splitLongText(heading+":\n\n"+transcript, 3500) {
		if _, err := telegramBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          a.managerChatID,
			Text:            part,
			ReplyParameters: &models.ReplyParameters{MessageID: messageID, AllowSendingWithoutReply: true},
		}); err != nil {
			logTelegramError("send final session history", err)
			return
		}
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
		"медицин", "травм", "аллерг", "страхов", "маляри", "вакцин", "привив", "лекарств",
		"диет", "веган", "вегетари", "питан", "экипиров", "инвентар", "струн", "багаж",
		"ранний заезд", "поздний выезд", "нестандартн", "оператор", "поговорить с человеком", "менеджер",
	} {
		if strings.Contains(question, trigger) {
			return true
		}
	}
	if strings.Contains(question, "ракетк") {
		for _, trigger := range []string{"нужно", "нужна", "брать", "взять", "свою", "предостав", "выдают", "аренд"} {
			if strings.Contains(question, trigger) {
				return true
			}
		}
	}
	return strings.Contains(question, "индивидуальн") &&
		(strings.Contains(question, "услов") || strings.Contains(question, "размещ"))
}

func ticketCard(current ticket, from *models.User) string {
	section := current.section
	if section == "" {
		section = "Общий вопрос"
	}

	lines := []string{
		"Новая заявка",
		"",
		fmt.Sprintf("Заявка №%d", current.id),
		"Раздел: " + section,
	}
	if current.topic != "" {
		lines = append(lines, "Тема: "+current.topic)
	}
	if current.campTitle != "" {
		lines = append(lines, "Кэмп: "+current.campTitle)
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
		lines = append(lines, "", "Ответ ИИ-помощника: "+current.aiAnswer)
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

func startTyping(parent context.Context, telegramBot *bot.Bot, chatID int64) context.CancelFunc {
	ctx, cancel := context.WithCancel(parent)
	send := func() {
		if _, err := telegramBot.SendChatAction(ctx, &bot.SendChatActionParams{ChatID: chatID, Action: models.ChatActionTyping}); err != nil && ctx.Err() == nil {
			logTelegramError("send typing action", err)
		}
	}
	send()
	go func() {
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				send()
			}
		}
	}()
	return cancel
}

func sendMessage(ctx context.Context, telegramBot *bot.Bot, params *bot.SendMessageParams) {
	if _, err := telegramBot.SendMessage(ctx, params); err != nil {
		logTelegramError("send Telegram message", err)
	}
}

func mainMenuKeyboard() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{
			{Text: trainingButtonText, CallbackData: trainingCallbackData},
			{Text: campsButtonText, CallbackData: campsCallbackData},
		},
		{
			{Text: aboutButtonText, CallbackData: aboutCallbackData},
			{Text: operatorButtonText, CallbackData: operatorCallbackData},
		},
	}}
}

func campsMenuKeyboard() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{
			{Text: camps[0].label, CallbackData: campCallbackPrefix + camps[0].id},
			{Text: camps[1].label, CallbackData: campCallbackPrefix + camps[1].id},
		},
		{{Text: camps[2].label, CallbackData: campCallbackPrefix + camps[2].id}},
		{{Text: operatorButtonText, CallbackData: operatorCallbackData}},
		{{Text: menuButtonText, CallbackData: menuCallbackData}},
	}}
}

func trainingKeyboard() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{
			{Text: trainingTopics[0].label, CallbackData: trainingTopicCallbackPrefix + trainingTopics[0].id},
			{Text: trainingTopics[1].label, CallbackData: trainingTopicCallbackPrefix + trainingTopics[1].id},
		},
		{
			{Text: trainingTopics[2].label, CallbackData: trainingTopicCallbackPrefix + trainingTopics[2].id},
			{Text: trainingTopics[3].label, CallbackData: trainingTopicCallbackPrefix + trainingTopics[3].id},
		},
		{
			{Text: trainingTopics[4].label, CallbackData: trainingTopicCallbackPrefix + trainingTopics[4].id},
			{Text: trainingTopics[5].label, CallbackData: trainingTopicCallbackPrefix + trainingTopics[5].id},
		},
		{{Text: trainingTopics[6].label, CallbackData: trainingTopicCallbackPrefix + trainingTopics[6].id}},
		{{Text: operatorButtonText, CallbackData: operatorCallbackData}},
		{{Text: menuButtonText, CallbackData: menuCallbackData}},
	}}
}

func trainingTopicKeyboard() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: backToTrainingButtonText, CallbackData: trainingCallbackData}},
		{{Text: operatorButtonText, CallbackData: operatorCallbackData}},
		{{Text: menuButtonText, CallbackData: menuCallbackData}},
	}}
}

func selectedCampKeyboard(campID string) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{
			{Text: campTopics[0].label, CallbackData: campTopicCallbackPrefix + campID + ":" + campTopics[0].id},
			{Text: campTopics[1].label, CallbackData: campTopicCallbackPrefix + campID + ":" + campTopics[1].id},
		},
		{
			{Text: campTopics[2].label, CallbackData: campTopicCallbackPrefix + campID + ":" + campTopics[2].id},
			{Text: campTopics[3].label, CallbackData: campTopicCallbackPrefix + campID + ":" + campTopics[3].id},
		},
		{{Text: campTopics[4].label, CallbackData: campTopicCallbackPrefix + campID + ":" + campTopics[4].id}},
		{{Text: backToCampsButtonText, CallbackData: campsCallbackData}},
		{{Text: operatorButtonText, CallbackData: operatorCallbackData}},
		{{Text: menuButtonText, CallbackData: menuCallbackData}},
	}}
}

func campTopicKeyboard(campID string) *models.InlineKeyboardMarkup {
	return selectedCampKeyboard(campID)
}

func campInfoKeyboard(campID string) *models.InlineKeyboardMarkup {
	return selectedCampKeyboard(campID)
}

func contextKeyboard(state chatState) *models.InlineKeyboardMarkup {
	switch {
	case state.section == sectionTraining:
		return trainingKeyboard()
	case state.section == sectionCamps && state.campID != "":
		return selectedCampKeyboard(state.campID)
	default:
		return operatorAndMenuKeyboard()
	}
}

func operatorKeyboard() *models.InlineKeyboardMarkup {
	return operatorAndMenuKeyboard()
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
