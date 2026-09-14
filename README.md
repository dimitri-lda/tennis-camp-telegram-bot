# Tennis Camp Telegram Bot

Telegram bot for the Dzala tennis camps. It answers client questions from the
`knowledge/dzala.md` knowledge base and hands the conversation over to a human
operator when the knowledge base is not allowed to answer.

## Requirements

- Go 1.27.1

## Local setup

1. Copy `.env.example` to `.env` if the local file does not exist.
2. Fill in the environment variables:

   | Variable | Required | Description |
   | --- | --- | --- |
   | `TELEGRAM_BOT_TOKEN` | yes | Bot token from @BotFather. |
   | `TELEGRAM_MANAGER_CHAT_ID` | no | ID of the private operator group. Without it the operator handoff is disabled. |
   | `OPENROUTER_API_KEY` | no | OpenRouter key. Without it every question goes to the operators. |
   | `OPENROUTER_MODEL` | no | Model name, `openrouter/free` by default. |
   | `KNOWLEDGE_FILE` | no | Knowledge base path, `knowledge/dzala.md` by default. |

3. Run the bot in polling mode from the repository root, so that the default
   knowledge base path resolves:

   ```bash
   go run ./cmd/bot
   ```

The knowledge base is read once at startup. The bot refuses to start when the
file is missing or empty.

## Operator group

1. Create a private Telegram group for the operators and add the bot to it.
2. Send `/chatid` in the group; the bot replies with the group ID.
3. Put that ID into `TELEGRAM_MANAGER_CHAT_ID` and restart the bot.

## Client flow

1. `/start` starts a session and shows the main menu: three camps, «О Dzala»
   and «Задать вопрос». A plain text message sent before `/start` starts a session,
   shows the menu and is then processed as the first question.
2. Choosing a camp stores it for the session and shows «О кэмпе», «Связаться с
   оператором» and «Главное меню». «О кэмпе» opens a short card from the knowledge
   base, followed by an option to view detailed information.
3. Any plain text message is treated as a question. The bot sends the knowledge
   base, selected camp, complete in-memory session history and current question to
   OpenRouter and expects a strict JSON action: `answer`, `clarify` or `handoff`.
4. `answer` and `clarify` replies carry operator and main-menu buttons. Questions
   about booking, availability, payment, discounts, refunds, cancellation, visas,
   flights, medical limitations or individual conditions go to an operator
   without an AI call.
5. If AI fails or cannot provide useful data, recognized camp and topic words are
   used to suggest the relevant «О кэмпе» button. Otherwise the client is offered
   an operator instead of a technical error.
6. `/exit` closes an active operator ticket, notifies the operator group and
   clears the current session. The next plain text message starts a new session.

## Operator flow

1. A handoff posts a ticket card and the complete current session transcript into
   the operator group with «Взять заявку» and «Закрыть заявку» buttons.
2. The first operator who presses «Взять заявку» becomes responsible; the card is
   updated with their name and everyone else sees a notification that the ticket
   is already taken.
3. Client messages are relayed into the ticket thread. The responsible operator
   replies by replying to the ticket card or to a relayed client message, and the
   text is delivered to the client.
4. «Закрыть заявку» closes the dialog, tells the client about it, and the next
   client message is handled as a new question again.

Only text is supported in both directions for now. Photos, files and voice
messages get a short notice instead.

## Limitations

- Dialog and ticket state is kept in memory only, so active tickets, the selected
  camp and ticket numbering are lost on restart.
- Production will use a webhook in a later development stage; only polling is
  implemented.

## Checks

```bash
go fmt ./...
go vet ./...
go test ./...
```
