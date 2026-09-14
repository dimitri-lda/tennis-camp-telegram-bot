package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/joho/godotenv"
)

const (
	startMessage       = "Привет! Я помогу выбрать теннисный кэмп. Выберите направление:"
	campCallbackPrefix = "camp:"
	managerFallback    = "В материалах кэмпа этой информации нет. Передал вопрос менеджеру."
	openRouterEndpoint = "https://openrouter.ai/api/v1/chat/completions"
	maxQuestionLength  = 1000
)

type camp struct {
	name    string
	details string
}

type openRouterRequest struct {
	Model       string              `json:"model"`
	Messages    []openRouterMessage `json:"messages"`
	MaxTokens   int                 `json:"max_tokens"`
	Temperature float64             `json:"temperature"`
}

type openRouterMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openRouterResponse struct {
	Choices []struct {
		Message openRouterMessage `json:"message"`
	} `json:"choices"`
}

var (
	camps = map[string]camp{
		"tsinandali": {
			name:    "Цинандали, Грузия",
			details: "28.09–05.10\nДля участников 18+: 3–5 часов тенниса, турнир, спортивная психология и знакомство с винным регионом.",
		},
		"tbilisi": {
			name:    "Тбилиси, Грузия",
			details: "18.10–25.10\nПодходит для любого уровня: 3–5 часов тенниса, гольф и SPA в Paragraph Golf & Spa 5*.",
		},
		"cape_town": {
			name:    "Кейптаун, ЮАР",
			details: "07.11–15.11.2026\nТеннис, recovery и экскурсионная программа: Hermanus, Table Mountain и винодельни.",
		},
	}
	selectedCampByChat = make(map[int64]string)
	selectedCampMu     sync.RWMutex
	managerChatID      int64
	openRouterKey      string
	aiHTTPClient       = &http.Client{Timeout: 30 * time.Second}
)

func main() {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Fatalf("load .env: %v", err)
	}

	token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if token == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN is required")
	}

	var err error
	managerChatID, err = parseManagerChatID(os.Getenv("TELEGRAM_MANAGER_CHAT_ID"))
	if err != nil {
		log.Fatalf("TELEGRAM_MANAGER_CHAT_ID: %v", err)
	}
	openRouterKey = strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	telegramBot, err := bot.New(
		token,
		bot.WithDefaultHandler(questionHandler),
	)
	if err != nil {
		log.Fatalf("create Telegram bot: %v", err)
	}

	telegramBot.RegisterHandler(bot.HandlerTypeMessageText, "start", bot.MatchTypeCommand, startHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeMessageText, "chatid", bot.MatchTypeCommandStartOnly, chatIDHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeCallbackQueryData, campCallbackPrefix, bot.MatchTypePrefix, campHandler)

	log.Println("bot started in polling mode")
	telegramBot.Start(ctx)
	log.Println("bot stopped")
}

func startHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	if update == nil || update.Message == nil {
		return
	}

	_, err := telegramBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      update.Message.Chat.ID,
		Text:        startMessage,
		ReplyMarkup: campKeyboard(),
	})
	if err != nil {
		log.Printf("send /start response: %v", err)
	}
}

func campHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	if update == nil || update.CallbackQuery == nil {
		return
	}

	callback := update.CallbackQuery
	selectedCamp, ok := camps[strings.TrimPrefix(callback.Data, campCallbackPrefix)]
	if !ok {
		_, _ = telegramBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: callback.ID,
			Text:            "Кэмп не найден. Отправьте /start ещё раз.",
			ShowAlert:       true,
		})
		return
	}

	if _, err := telegramBot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: callback.ID,
	}); err != nil {
		log.Printf("answer camp selection: %v", err)
	}

	if callback.Message.Message == nil {
		return
	}
	rememberSelectedCamp(callback.Message.Message.Chat.ID, strings.TrimPrefix(callback.Data, campCallbackPrefix))

	_, err := telegramBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: callback.Message.Message.Chat.ID,
		Text:   selectedCamp.name + "\n\n" + selectedCamp.details,
	})
	if err != nil {
		log.Printf("send camp details: %v", err)
	}
}

func chatIDHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	if update == nil || update.Message == nil || update.Message.Chat.ID >= 0 {
		return
	}

	sendMessage(ctx, telegramBot, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   fmt.Sprintf("ID этой группы: %d", update.Message.Chat.ID),
	})
}

func questionHandler(ctx context.Context, telegramBot *bot.Bot, update *models.Update) {
	if update == nil || update.Message == nil || update.Message.Text == "" {
		return
	}

	message := update.Message
	if managerChatID != 0 && message.Chat.ID == managerChatID {
		return
	}

	campID, ok := selectedCamp(message.Chat.ID)
	if !ok {
		sendMessage(ctx, telegramBot, &bot.SendMessageParams{
			ChatID: message.Chat.ID,
			Text:   "Сначала выберите кэмп через /start.",
		})
		return
	}

	question := strings.TrimSpace(message.Text)
	if question == "" || len(question) > maxQuestionLength {
		sendMessage(ctx, telegramBot, &bot.SendMessageParams{
			ChatID: message.Chat.ID,
			Text:   "Вопрос должен содержать от 1 до 1000 символов.",
		})
		return
	}

	if requiresManager(question) || openRouterKey == "" {
		handoffToManager(ctx, telegramBot, message, campID, question)
		return
	}

	answer, err := askAI(ctx, campID, question)
	if err != nil || strings.Contains(strings.ToLower(answer), "нужно уточнить у менеджера") {
		handoffToManager(ctx, telegramBot, message, campID, question)
		return
	}

	sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: message.Chat.ID, Text: answer})
}

func handoffToManager(ctx context.Context, telegramBot *bot.Bot, message *models.Message, campID, question string) {
	if managerChatID == 0 {
		sendMessage(ctx, telegramBot, &bot.SendMessageParams{
			ChatID: message.Chat.ID,
			Text:   "Нужно уточнить у менеджера. Операторский чат пока не подключён.",
		})
		return
	}

	selectedCamp := camps[campID]
	client := "username не указан"
	if message.From.Username != "" {
		client = "@" + message.From.Username
	}

	if _, err := telegramBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: managerChatID,
		Text: "Новая заявка\n\n" +
			"Кэмп: " + selectedCamp.name + "\n" +
			"Клиент: " + client + "\n" +
			"Вопрос: " + question,
	}); err != nil {
		log.Printf("send manager lead: %v", err)
		return
	}

	sendMessage(ctx, telegramBot, &bot.SendMessageParams{ChatID: message.Chat.ID, Text: managerFallback})
}

func askAI(ctx context.Context, campID, question string) (string, error) {
	selectedCamp, ok := camps[campID]
	if !ok {
		return "", errors.New("camp is not selected")
	}

	prompt := "Ты консультант теннисных кэмпов Dzala. Отвечай по-русски, кратко, максимум четырьмя предложениями. " +
		"Используй только карточку кэмпа ниже. Не придумывай цены, наличие мест, перелёты, визы, оплату, возвраты или индивидуальные условия. " +
		"Если точного ответа нет, ответь строго: «Нужно уточнить у менеджера».\n\n" +
		"Карточка кэмпа:\n" + selectedCamp.name + "\n" + selectedCamp.details

	payload, err := json.Marshal(openRouterRequest{
		Model: "openrouter/free",
		Messages: []openRouterMessage{
			{Role: "system", Content: prompt},
			{Role: "user", Content: question},
		},
		MaxTokens:   220,
		Temperature: 0.2,
	})
	if err != nil {
		return "", err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, openRouterEndpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+openRouterKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Title", "Dzala Tennis Camp Bot")

	response, err := aiHTTPClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("OpenRouter returned HTTP %d", response.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", err
	}

	var result openRouterResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 {
		return "", errors.New("OpenRouter returned no choices")
	}

	answer := strings.TrimSpace(result.Choices[0].Message.Content)
	if answer == "" {
		return "", errors.New("OpenRouter returned an empty answer")
	}
	return answer, nil
}

func requiresManager(question string) bool {
	for _, trigger := range []string{
		"заброни", "оплат", "депозит", "скидк", "есть ли места", "свободные места",
		"возврат", "отмен", "виз", "нестандартн", "индивидуальн", "поговорить с человеком",
	} {
		if strings.Contains(strings.ToLower(question), trigger) {
			return true
		}
	}
	return false
}

func parseManagerChatID(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}

	chatID, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, errors.New("must be a Telegram chat ID")
	}
	return chatID, nil
}

func rememberSelectedCamp(chatID int64, campID string) {
	selectedCampMu.Lock()
	defer selectedCampMu.Unlock()
	selectedCampByChat[chatID] = campID
}

func selectedCamp(chatID int64) (string, bool) {
	selectedCampMu.RLock()
	defer selectedCampMu.RUnlock()
	campID, ok := selectedCampByChat[chatID]
	return campID, ok
}

func sendMessage(ctx context.Context, telegramBot *bot.Bot, params *bot.SendMessageParams) {
	if _, err := telegramBot.SendMessage(ctx, params); err != nil {
		log.Printf("send Telegram message: %v", err)
	}
}

func campKeyboard() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: "🇬🇪 Цинандали", CallbackData: campCallbackPrefix + "tsinandali"}},
			{{Text: "🇬🇪 Тбилиси", CallbackData: campCallbackPrefix + "tbilisi"}},
			{{Text: "🇿🇦 Кейптаун", CallbackData: campCallbackPrefix + "cape_town"}},
		},
	}
}
