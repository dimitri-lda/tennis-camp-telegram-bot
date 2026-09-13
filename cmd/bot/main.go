package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/joho/godotenv"
)

const (
	startMessage       = "Привет! Я помогу выбрать теннисный кэмп. Выберите направление:"
	campCallbackPrefix = "camp:"
)

type camp struct {
	name    string
	details string
}

var camps = map[string]camp{
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

func main() {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Fatalf("load .env: %v", err)
	}

	token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if token == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN is required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	telegramBot, err := bot.New(
		token,
		bot.WithDefaultHandler(func(context.Context, *bot.Bot, *models.Update) {}),
	)
	if err != nil {
		log.Fatalf("create Telegram bot: %v", err)
	}

	telegramBot.RegisterHandler(bot.HandlerTypeMessageText, "start", bot.MatchTypeCommand, startHandler)
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

	_, err := telegramBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: callback.Message.Message.Chat.ID,
		Text:   selectedCamp.name + "\n\n" + selectedCamp.details,
	})
	if err != nil {
		log.Printf("send camp details: %v", err)
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
