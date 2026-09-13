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

The bot runs in polling mode for local development. Open it in Telegram and send `/start`; it should offer three camp destinations. Select a destination to receive its short description. Stop the bot with `Ctrl+C`.

Production will use a webhook in a later development stage.

## Checks

```bash
go fmt ./...
go vet ./...
go test ./...
```
