package main

import (
	"errors"
	"strings"
	"testing"

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
	}

	for _, test := range tests {
		if got := requiresOperator(test.question); got != test.want {
			t.Errorf("requiresOperator(%q) = %t, want %t", test.question, got, test.want)
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

func TestAIFailureResponseWithoutOperatorsHasNoButton(t *testing.T) {
	params := aiFailureResponse(7, false)
	if params.Text != operatorsDownMessage {
		t.Errorf("text = %q, want %q", params.Text, operatorsDownMessage)
	}
	if params.ReplyMarkup != nil {
		t.Error("ReplyMarkup is not nil without TELEGRAM_MANAGER_CHAT_ID")
	}

	params = aiFailureResponse(7, true)
	if params.Text != aiFailureMessage || params.ReplyMarkup == nil {
		t.Errorf("configured response = %+v, want AI failure text and operator button", params)
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
