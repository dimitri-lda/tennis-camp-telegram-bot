# Tennis Camp Telegram Bot

Telegram bot for Dzala Tennis School, regular tennis and padel training, and
Dzala tennis camps. It answers client questions from the `knowledge/dzala.md`
knowledge base and hands the conversation over to a human operator when the
knowledge base is not allowed to answer.

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
   | `OPENROUTER_MODEL` | no | Model name, `nvidia/nemotron-3-super-120b-a12b:free` by default. |
   | `KNOWLEDGE_FILE` | no | Knowledge base path, `knowledge/dzala.md` by default. |

3. Run the bot in polling mode from the repository root, so that the default
   knowledge base path resolves:

   ```bash
   /Users/dimitri_lda/sdk/go1.27.1/bin/go run ./cmd/bot
   ```

The knowledge base is read once at startup. The bot refuses to start when the
file is missing or empty. It separates school information, regular training,
padel and each camp, and marks prices, promotions, schedules and team composition
as changeable. A separately labelled practical destination FAQ is general
reference information, not a Dzala condition.

## Choosing a model

The default is the fixed free model used for this project:

```env
OPENROUTER_MODEL=nvidia/nemotron-3-super-120b-a12b:free
```

OpenRouter advertises support for `response_format` and reasoning parameters for
this model. The first attempt asks for a strict JSON schema and excludes reasoning
from the answer budget. An unparsable answer triggers one retry in plain JSON mode
without provider restrictions. Failed attempts log only the model name and a safe
error summary; API keys and client data are never logged.

## Operator group

1. Create a private Telegram group for the operators and add the bot to it.
2. Send `/chatid` in the group; the bot replies with the group ID.
3. Put that ID into `TELEGRAM_MANAGER_CHAT_ID` and restart the bot.

## Client flow

1. `/start` introduces «ИИ-помощник Dzala» and shows «Тренировки», «Кэмпы»,
   «О Dzala» and «Связаться с оператором». `/start` and `/exit` are registered in
   Telegram's slash-command menu. A plain text message sent before `/start` starts
   a session, shows the menu and is then processed as the first question.
2. «Тренировки» opens topic buttons for tennis, padel, children, adults, coaches,
   prices and locations. Topic cards keep back-to-training, operator and main-menu
   navigation available.
3. «Кэмпы» opens the three destinations. Choosing one immediately shows its short
   card and topic buttons for program, dates and price, accommodation, coaches and
   practical information. Every camp card has back-to-camps navigation.
4. Any plain text message is treated as a question. While OpenRouter is working,
   Telegram shows a typing action. The request contains the knowledge base, active
   section, selected topic, selected camp, bounded session history and current
   question, and expects `answer`, `clarify` or `handoff`.
5. `answer` and `clarify` replies carry contextual training or camp actions.
   Questions about booking, availability, payment, discounts, refunds,
   cancellation, visas, flights, medical limitations or individual conditions go
   to an operator without an AI call.
6. An operator ticket includes the selected section, topic and camp together with
   the question and complete transcript. If the AI request fails, times out or
   returns an invalid response, the ticket is created immediately.
7. When AI is not configured, recognized camp and topic words may still use a
   local knowledge-based hint. `/exit` closes an active operator ticket, notifies
   the operator group and clears the current session.

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
- Only the last 20 session messages are sent to the AI; operators still receive
  the complete session transcript.
- Production will use a webhook in a later development stage; only polling is
  implemented.

## Checks

```bash
go fmt ./...
go vet ./...
go test ./...
```
