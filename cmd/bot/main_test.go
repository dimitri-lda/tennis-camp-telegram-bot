package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func TestRequiresOperator(t *testing.T) {
	tests := []struct {
		question string
		want     bool
	}{
		{question: "Есть ли свободные места?", want: true},
		{question: "Можно оплатить депозит?", want: true},
		{question: "Нужна ли виза в ЮАР?", want: true},
		{question: "Какие условия отмены?", want: true},
		{question: "Есть ли медицинские ограничения?", want: true},
		{question: "Хочу забронировать кэмп в Тбилиси", want: true},
		{question: "Сколько часов тенниса в день?", want: false},
		{question: "Где живут участники кэмпа?", want: false},
		{question: "Есть ли индивидуальные тренировки?", want: false},
		{question: "Нужны индивидуальные условия", want: true},
		{question: "Доступно индивидуальное размещение?", want: true},
		{question: "Есть ли комары в Кейптауне?", want: false},
		{question: "Нужны ли таблетки от малярии?", want: true},
		{question: "Какое будет питание?", want: true},
		{question: "Нужно ли брать ракетку?", want: true},
		{question: "Какой ракеткой мира была Натела?", want: false},
	}

	for _, test := range tests {
		if got := requiresOperator(test.question); got != test.want {
			t.Errorf("requiresOperator(%q) = %t, want %t", test.question, got, test.want)
		}
	}
}

func TestUnsupportedContentIgnoresServiceMessages(t *testing.T) {
	if hasUnsupportedContent(&models.Message{NewChatMembers: []models.User{{ID: 1}}}) {
		t.Error("service message was treated as unsupported user content")
	}
	if !hasUnsupportedContent(&models.Message{Photo: []models.PhotoSize{{}}}) {
		t.Error("photo was not treated as unsupported user content")
	}
	if !hasUnsupportedContent(&models.Message{Voice: &models.Voice{}}) {
		t.Error("voice message was not treated as unsupported user content")
	}
}

func TestLocalFallbackMatchesCampAndTopic(t *testing.T) {
	tests := []struct {
		question       string
		selectedCampID string
		wantCampID     string
		wantTopic      string
		wantOK         bool
	}{
		{question: "Расскажи стоимость Тбилиси", wantCampID: "tbilisi", wantTopic: "стоимость", wantOK: true},
		{question: "Какие там тренировки?", selectedCampID: "cape_town", wantCampID: "cape_town", wantTopic: "тренировки", wantOK: true},
		{question: "Что такое Ценандали?", wantCampID: "tsinandali", wantOK: true},
		{question: "Хочу задать вопрос", wantOK: false},
		{question: "Совсем непонятный запрос", wantOK: false},
	}

	for _, test := range tests {
		hint, ok := matchLocalFallback(test.question, test.selectedCampID)
		if ok != test.wantOK || hint.campID != test.wantCampID || hint.topic != test.wantTopic {
			t.Errorf("matchLocalFallback(%q) = %+v, %t, want camp=%q topic=%q ok=%t", test.question, hint, ok, test.wantCampID, test.wantTopic, test.wantOK)
		}
	}
}

func TestLocalFallbackReplyRules(t *testing.T) {
	if _, _, ok := localFallbackReply(knowledgeFixture, "Хочу забронировать Тбилиси", "tbilisi"); ok {
		t.Error("sensitive booking question must not be answered with a camp card")
	}

	text, keyboard, ok := localFallbackReply(knowledgeFixture, "В какой части планеты?", "tbilisi")
	if !ok {
		t.Fatal("localFallbackReply() ok = false, want a hint for the selected camp")
	}
	if !strings.Contains(text, "Тбилиси, Грузия") {
		t.Errorf("text = %q, want the selected camp title", text)
	}
	if keyboard.InlineKeyboard[0][0].CallbackData != campInfoCallbackPrefix+"tbilisi" {
		t.Errorf("keyboard = %+v, want the camp info button", keyboard.InlineKeyboard[0][0])
	}

	trainingText, trainingMenu, ok := localFallbackReply(knowledgeFixture, "Какие есть тренировки?", "")
	if !ok || !strings.Contains(trainingText, "Тренировки") || trainingMenu.InlineKeyboard[0][1].CallbackData != trainingCallbackData {
		t.Errorf("training fallback = %q, %+v, %t", trainingText, trainingMenu, ok)
	}

	if _, _, ok := localFallbackReply(knowledgeFixture, "Совсем непонятный запрос", ""); ok {
		t.Error("localFallbackReply() ok = true, want false without a camp or topic")
	}
}

func TestFormatSessionHistory(t *testing.T) {
	history := []sessionMessage{
		{role: sessionRoleUser, text: "Расскажите про Тбилиси"},
		{role: sessionRoleAssistant, text: "Кэмп проходит в Тбилиси."},
		{role: sessionRoleOperator, text: "Добрый день!"},
	}
	formatted := formatSessionHistory(history)
	for _, want := range []string{"Клиент: Расскажите про Тбилиси", "Бот: Кэмп проходит в Тбилиси.", "Оператор: Добрый день!"} {
		if !strings.Contains(formatted, want) {
			t.Errorf("formatSessionHistory() = %q, want %q", formatted, want)
		}
	}
}

func TestQuestionExceedsLimitCountsCharacters(t *testing.T) {
	if questionExceedsLimit(strings.Repeat("я", maxQuestionLength)) {
		t.Error("questionExceedsLimit() = true for exactly 1000 Cyrillic characters")
	}
	if !questionExceedsLimit(strings.Repeat("я", maxQuestionLength+1)) {
		t.Error("questionExceedsLimit() = false for 1001 Cyrillic characters")
	}
}

func TestPlanQuestion(t *testing.T) {
	tests := []struct {
		name             string
		question         string
		aiEnabled        bool
		operatorsEnabled bool
		want             questionPlan
	}{
		{name: "regular question goes to AI", question: "Сколько часов тенниса?", aiEnabled: true, operatorsEnabled: true, want: planAI},
		{name: "booking goes to an operator", question: "Хочу забронировать", aiEnabled: true, operatorsEnabled: true, want: planOperator},
		{name: "no OPENROUTER_API_KEY goes to an operator", question: "Сколько часов тенниса?", operatorsEnabled: true, want: planOperator},
		{name: "no TELEGRAM_MANAGER_CHAT_ID for booking", question: "Хочу забронировать", aiEnabled: true, want: planUnavailable},
		{name: "nothing configured", question: "Сколько часов тенниса?", want: planUnavailable},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := planQuestion(test.question, test.aiEnabled, test.operatorsEnabled); got != test.want {
				t.Errorf("planQuestion() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestParseManagerChatID(t *testing.T) {
	chatID, err := parseManagerChatID("  ")
	if err != nil {
		t.Fatalf("parseManagerChatID() error = %v, want nil for a missing value", err)
	}
	if chatID != 0 {
		t.Errorf("parseManagerChatID() = %d, want 0 for a missing value", chatID)
	}

	if chatID, err = parseManagerChatID(" -1001234567890 "); err != nil || chatID != -1001234567890 {
		t.Errorf("parseManagerChatID() = %d, %v, want -1001234567890, nil", chatID, err)
	}
	if _, err = parseManagerChatID("group"); err == nil {
		t.Error("parseManagerChatID() error = nil, want an error for a non-numeric value")
	}
}

func TestOperatorsDisabledKeepsBotAlive(t *testing.T) {
	application := &app{store: newStore(), knowledge: knowledgeFixture, ai: newAIClient("", "")}
	if application.operatorsEnabled() {
		t.Error("operatorsEnabled() = true, want false without TELEGRAM_MANAGER_CHAT_ID")
	}
	if got := planQuestion("Сколько часов тенниса?", application.ai.enabled(), application.operatorsEnabled()); got != planUnavailable {
		t.Errorf("planQuestion() = %d, want planUnavailable", got)
	}
}

func TestAIFailureImmediatelyCreatesOperatorTicket(t *testing.T) {
	aiServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"choices":[{"message":{"content":"не JSON"}}]}`))
	}))
	defer aiServer.Close()

	telegramServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ok":true,"result":{"message_id":1,"date":0,"chat":{"id":-100,"type":"supergroup"},"text":"ok"}}`))
	}))
	defer telegramServer.Close()

	telegramBot, err := bot.New("1:test", bot.WithSkipGetMe(), bot.WithServerURL(telegramServer.URL))
	if err != nil {
		t.Fatalf("bot.New() error = %v", err)
	}
	client := newAIClient("test-key", "test-model")
	client.endpoint = aiServer.URL
	application := &app{store: newStore(), knowledge: knowledgeFixture, ai: client, managerChatID: -100}
	application.store.startSession(7)

	application.answerQuestion(context.Background(), telegramBot, 7, &models.User{ID: 7, FirstName: "Дмитрий"}, "Кто тренер?")

	current, active := application.store.activeTicket(7)
	if !active {
		t.Fatal("activeTicket() = false, want an operator ticket after AI failure")
	}
	if current.question != "Кто тренер?" || current.status != ticketOpen {
		t.Errorf("ticket = %+v, want the original question and open status", current)
	}
}

func TestTicketCard(t *testing.T) {
	current := ticket{id: 3, campTitle: "Тбилиси, Грузия", question: "Есть ли места?", aiAnswer: "Уточните у оператора."}
	card := ticketCard(current, &models.User{FirstName: "Иван", Username: "ivan"})

	for _, want := range []string{"Новая заявка", "Заявка №3", "Кэмп: Тбилиси, Грузия", "Имя: Иван", "Telegram: @ivan", "Вопрос: Есть ли места?", "Ответ ИИ: Уточните у оператора."} {
		if !strings.Contains(card, want) {
			t.Errorf("ticketCard() = %q, want it to contain %q", card, want)
		}
	}

	card = ticketCard(ticket{id: 4, question: "Вопрос"}, nil)
	if !strings.Contains(card, "Кэмп: не выбран") {
		t.Errorf("ticketCard() = %q, want it to report a missing camp", card)
	}
	if strings.Contains(card, "Ответ ИИ") {
		t.Errorf("ticketCard() = %q, want no AI section when there is no AI answer", card)
	}
}

func TestClosedTicketCardKeepsAssignedOperator(t *testing.T) {
	current := ticket{id: 3, question: "Вопрос", operatorID: 11, operatorName: "Оксана", status: ticketClosed}
	current.card = ticketCard(current, nil)

	card := ticketCardWithStatus(current)
	for _, want := range []string{"Заявку взял: Оксана", "Заявка закрыта."} {
		if !strings.Contains(card, want) {
			t.Errorf("ticketCardWithStatus() = %q, want it to contain %q", card, want)
		}
	}
}

func TestTelegramErrorSummaryDoesNotExposeErrorText(t *testing.T) {
	const secret = "https://api.telegram.org/botSECRET/getUpdates"
	summary := telegramErrorSummary(errors.New(secret))
	if strings.Contains(summary, "SECRET") || strings.Contains(summary, "api.telegram.org") {
		t.Errorf("telegramErrorSummary() leaked sensitive URL: %q", summary)
	}
}

func TestBotCommandsIncludeStartAndExit(t *testing.T) {
	commands := botCommands()
	if len(commands) != 2 {
		t.Fatalf("botCommands() returned %d commands, want 2", len(commands))
	}
	if commands[0].Command != "start" || commands[1].Command != "exit" {
		t.Errorf("botCommands() = %+v, want start and exit", commands)
	}
}

func TestMainMenuAndCampSubmenu(t *testing.T) {
	mainMenu := mainMenuKeyboard().InlineKeyboard
	if len(mainMenu) != 2 {
		t.Fatalf("mainMenuKeyboard() rows = %d, want 2", len(mainMenu))
	}
	callbacks := []string{
		mainMenu[0][0].CallbackData,
		mainMenu[0][1].CallbackData,
		mainMenu[1][0].CallbackData,
		mainMenu[1][1].CallbackData,
	}
	want := []string{campsCallbackData, trainingCallbackData, askCallbackData, aboutCallbackData}
	if strings.Join(callbacks, ",") != strings.Join(want, ",") {
		t.Errorf("main menu callbacks = %v, want %v", callbacks, want)
	}

	campMenu := campsMenuKeyboard().InlineKeyboard
	if len(campMenu) != 3 || campMenu[0][0].CallbackData != campCallbackPrefix+"tsinandali" || campMenu[2][0].CallbackData != menuCallbackData {
		t.Errorf("campsMenuKeyboard() = %+v", campMenu)
	}
}

func TestTrainingKeyboardContainsOperatorAndMenu(t *testing.T) {
	keyboard := trainingKeyboard().InlineKeyboard
	if len(keyboard) != 2 || keyboard[0][0].CallbackData != operatorCallbackData || keyboard[1][0].CallbackData != menuCallbackData {
		t.Errorf("trainingKeyboard() = %+v", keyboard)
	}
}

func TestStartAndMenuMessagesInviteDirectAIQuestion(t *testing.T) {
	for name, message := range map[string]string{"start": startMessage, "menu": menuMessage} {
		if !strings.Contains(message, aiPromptMessage) {
			t.Errorf("%s message does not contain AI prompt", name)
		}
	}
}

func TestCampKeyboardsKeepMainMenuAvailable(t *testing.T) {
	selected := selectedCampKeyboard("tbilisi").InlineKeyboard
	if len(selected) != 3 || selected[0][0].CallbackData != campInfoCallbackPrefix+"tbilisi" || selected[2][0].CallbackData != menuCallbackData {
		t.Errorf("selectedCampKeyboard() = %+v", selected)
	}

	info := campInfoKeyboard("tbilisi").InlineKeyboard
	if len(info) != 3 || info[0][0].CallbackData != campDetailsCallbackPrefix+"tbilisi" || info[2][0].CallbackData != menuCallbackData {
		t.Errorf("campInfoKeyboard() = %+v", info)
	}
}

func TestSplitLongTextKeepsEveryPartWithinLimit(t *testing.T) {
	parts := splitLongText(strings.Repeat("я", 25), 10)
	if len(parts) != 3 {
		t.Fatalf("splitLongText() returned %d parts, want 3", len(parts))
	}
	if strings.Join(parts, "") != strings.Repeat("я", 25) {
		t.Error("splitLongText() lost content")
	}
	for _, part := range parts {
		if utf8.RuneCountInString(part) > 10 {
			t.Errorf("part length = %d, want at most 10", utf8.RuneCountInString(part))
		}
	}
}

func TestOperatorName(t *testing.T) {
	tests := []struct {
		from models.User
		want string
	}{
		{from: models.User{FirstName: "Оксана", LastName: "П"}, want: "Оксана П"},
		{from: models.User{Username: "oksana"}, want: "@oksana"},
		{from: models.User{}, want: "оператор"},
	}

	for _, test := range tests {
		if got := operatorName(test.from); got != test.want {
			t.Errorf("operatorName() = %q, want %q", got, test.want)
		}
	}
}
