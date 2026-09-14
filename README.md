# Tennis Camp Telegram Bot

Minimal Go project scaffold for a tennis camp Telegram bot.

## Requirements

- Go 1.27.1

## Local setup

1. Copy `.env.example` to `.env` if the local file does not exist.
2. Fill in the required environment variables.
3. Run the bot entry point:

   ```bash
   go run ./cmd/bot
   ```

The bot runs in polling mode for local development. Open it in Telegram, send `/start`, choose a camp, and ask a question in plain text. The bot uses only the selected camp's card as AI context. Questions about booking, payment, visas, availability, or missing facts are sent to the operator chat when `TELEGRAM_MANAGER_CHAT_ID` is configured.

To enable test AI, create an OpenRouter API key and set `OPENROUTER_API_KEY` in `.env`. The bot uses the `openrouter/free` model router, which is suitable only for low-volume testing.

Production will use a webhook in a later development stage.

## Checks

```bash
go fmt ./...
go vet ./...
go test ./...
```
