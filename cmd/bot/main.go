package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/go-telegram/bot"
	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Fatalf("load .env: %v", err)
	}

	token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if token == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN is required")
	}

	managerChatID, err := parseManagerChatID(os.Getenv("TELEGRAM_MANAGER_CHAT_ID"))
	if err != nil {
		log.Fatalf("TELEGRAM_MANAGER_CHAT_ID: %v", err)
	}

	knowledge, err := loadKnowledge(os.Getenv("KNOWLEDGE_FILE"))
	if err != nil {
		log.Fatalf("knowledge base is required: %v", err)
	}

	application := &app{
		store:         newStore(),
		knowledge:     knowledge,
		ai:            newAIClient(os.Getenv("OPENROUTER_API_KEY"), os.Getenv("OPENROUTER_MODEL")),
		managerChatID: managerChatID,
	}
	if !application.ai.enabled() {
		log.Println("OPENROUTER_API_KEY is not set: questions go straight to operators")
	}
	if !application.operatorsEnabled() {
		log.Println("TELEGRAM_MANAGER_CHAT_ID is not set: operator handoff is disabled")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	telegramBot, err := bot.New(
		token,
		bot.WithDefaultHandler(application.messageHandler),
		bot.WithErrorsHandler(func(err error) { logTelegramError("Telegram polling", err) }),
	)
	if err != nil {
		log.Fatalf("create Telegram bot: %s", telegramErrorSummary(err))
	}

	telegramBot.RegisterHandler(bot.HandlerTypeMessageText, "start", bot.MatchTypeCommand, application.startHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeMessageText, "chatid", bot.MatchTypeCommandStartOnly, application.chatIDHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeCallbackQueryData, campCallbackPrefix, bot.MatchTypePrefix, application.campHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeCallbackQueryData, aboutCallbackData, bot.MatchTypeExact, application.aboutHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeCallbackQueryData, askCallbackData, bot.MatchTypeExact, application.askHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeCallbackQueryData, operatorCallbackData, bot.MatchTypeExact, application.operatorHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeCallbackQueryData, menuCallbackData, bot.MatchTypeExact, application.menuHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeCallbackQueryData, takeTicketPrefix, bot.MatchTypePrefix, application.takeTicketHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeCallbackQueryData, closeTicketPrefix, bot.MatchTypePrefix, application.closeTicketHandler)

	log.Println("bot started in polling mode")
	telegramBot.Start(ctx)
	log.Println("bot stopped")
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
