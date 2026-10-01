package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/joho/godotenv"
)

const (
	defaultAISessionIdleTimeout       = 24 * time.Hour
	defaultOperatorSessionIdleTimeout = 48 * time.Hour
	defaultSessionSweepInterval       = 10 * time.Minute
	defaultAIQuestionInterval         = 5 * time.Second
	defaultAIMaxQuestions             = 30
	defaultFallbackModel              = "liquid/lfm-2.5-2.6b:free"
)

type runtimeConfig struct {
	aiSessionIdleTimeout       time.Duration
	operatorSessionIdleTimeout time.Duration
	sessionSweepInterval       time.Duration
	aiQuestionInterval         time.Duration
	aiMaxQuestions             int
	fallbackModel              string
}

func parseDurationOrDefault(value string, fallback time.Duration) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("must be a positive Go duration")
	}
	return parsed, nil
}

func parsePositiveIntOrDefault(value string, fallback int) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("must be a positive integer")
	}
	return parsed, nil
}

func loadRuntimeConfig() (runtimeConfig, error) {
	var config runtimeConfig
	var err error
	if config.aiSessionIdleTimeout, err = parseDurationOrDefault(os.Getenv("AI_SESSION_IDLE_TIMEOUT"), defaultAISessionIdleTimeout); err != nil {
		return runtimeConfig{}, fmt.Errorf("AI_SESSION_IDLE_TIMEOUT: %w", err)
	}
	if config.operatorSessionIdleTimeout, err = parseDurationOrDefault(os.Getenv("OPERATOR_SESSION_IDLE_TIMEOUT"), defaultOperatorSessionIdleTimeout); err != nil {
		return runtimeConfig{}, fmt.Errorf("OPERATOR_SESSION_IDLE_TIMEOUT: %w", err)
	}
	if config.sessionSweepInterval, err = parseDurationOrDefault(os.Getenv("SESSION_SWEEP_INTERVAL"), defaultSessionSweepInterval); err != nil {
		return runtimeConfig{}, fmt.Errorf("SESSION_SWEEP_INTERVAL: %w", err)
	}
	if config.aiQuestionInterval, err = parseDurationOrDefault(os.Getenv("AI_MIN_QUESTION_INTERVAL"), defaultAIQuestionInterval); err != nil {
		return runtimeConfig{}, fmt.Errorf("AI_MIN_QUESTION_INTERVAL: %w", err)
	}
	if config.aiMaxQuestions, err = parsePositiveIntOrDefault(os.Getenv("AI_MAX_QUESTIONS_PER_SESSION"), defaultAIMaxQuestions); err != nil {
		return runtimeConfig{}, fmt.Errorf("AI_MAX_QUESTIONS_PER_SESSION: %w", err)
	}
	config.fallbackModel = strings.TrimSpace(os.Getenv("OPENROUTER_FALLBACK_MODEL"))
	if config.fallbackModel == "" {
		config.fallbackModel = defaultFallbackModel
	}
	return config, nil
}

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
	config, err := loadRuntimeConfig()
	if err != nil {
		log.Fatalf("configuration: %v", err)
	}

	application := &app{
		store:                      newStore(),
		knowledge:                  knowledge,
		ai:                         newAIClientWithFallback(os.Getenv("OPENROUTER_API_KEY"), os.Getenv("OPENROUTER_MODEL"), config.fallbackModel),
		managerChatID:              managerChatID,
		aiSessionIdleTimeout:       config.aiSessionIdleTimeout,
		operatorSessionIdleTimeout: config.operatorSessionIdleTimeout,
		sessionSweepInterval:       config.sessionSweepInterval,
		aiQuestionInterval:         config.aiQuestionInterval,
		aiMaxQuestions:             config.aiMaxQuestions,
		stats:                      &botStats{},
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
	if err := setBotCommands(ctx, telegramBot); err != nil {
		logTelegramError("set Telegram commands", err)
	}

	telegramBot.RegisterHandler(bot.HandlerTypeMessageText, "start", bot.MatchTypeCommand, application.startHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeMessageText, "help", bot.MatchTypeCommand, application.helpHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeMessageText, "exit", bot.MatchTypeCommand, application.exitHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeMessageText, "chatid", bot.MatchTypeCommandStartOnly, application.chatIDHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeMessageText, "stats", bot.MatchTypeCommandStartOnly, application.statsHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeCallbackQueryData, campCallbackPrefix, bot.MatchTypePrefix, application.campHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeCallbackQueryData, campInfoCallbackPrefix, bot.MatchTypePrefix, application.campInfoHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeCallbackQueryData, campDetailsCallbackPrefix, bot.MatchTypePrefix, application.campDetailsHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeCallbackQueryData, campTopicCallbackPrefix, bot.MatchTypePrefix, application.campTopicHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeCallbackQueryData, campsCallbackData, bot.MatchTypeExact, application.campsHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeCallbackQueryData, trainingCallbackData, bot.MatchTypeExact, application.trainingHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeCallbackQueryData, trainingTopicCallbackPrefix, bot.MatchTypePrefix, application.trainingTopicHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeCallbackQueryData, aboutCallbackData, bot.MatchTypeExact, application.aboutHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeCallbackQueryData, askCallbackData, bot.MatchTypeExact, application.askHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeCallbackQueryData, operatorCallbackData, bot.MatchTypeExact, application.operatorHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeCallbackQueryData, menuCallbackData, bot.MatchTypeExact, application.menuHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeCallbackQueryData, takeTicketPrefix, bot.MatchTypePrefix, application.takeTicketHandler)
	telegramBot.RegisterHandler(bot.HandlerTypeCallbackQueryData, closeTicketPrefix, bot.MatchTypePrefix, application.closeTicketHandler)

	go application.runSessionSweeper(ctx, telegramBot)
	log.Println("bot started in polling mode")
	telegramBot.Start(ctx)
	log.Println("bot stopped")
}

func botCommands() []models.BotCommand {
	return []models.BotCommand{
		{Command: "start", Description: "Начать новую сессию"},
		{Command: "help", Description: "Как пользоваться ботом"},
		{Command: "exit", Description: "Завершить текущую сессию"},
	}
}

func setBotCommands(ctx context.Context, telegramBot *bot.Bot) error {
	if _, err := telegramBot.SetMyCommands(ctx, &bot.SetMyCommandsParams{
		Commands: botCommands(),
		Scope:    &models.BotCommandScopeAllPrivateChats{},
	}); err != nil {
		return err
	}
	_, err := telegramBot.SetMyCommands(ctx, &bot.SetMyCommandsParams{
		Commands: []models.BotCommand{
			{Command: "chatid", Description: "Показать ID группы"},
			{Command: "stats", Description: "Безопасная статистика бота"},
		},
		Scope: &models.BotCommandScopeAllGroupChats{},
	})
	return err
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
