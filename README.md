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

The application code is intentionally minimal at this stage. Local development will use polling, while production will use a webhook.

## Checks

```bash
go fmt ./...
go vet ./...
go test ./...
```
