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
   | `OPENROUTER_MODEL` | no | Primary model, `nvidia/nemotron-3-super-120b-a12b:free` by default. |
   | `OPENROUTER_FALLBACK_MODEL` | no | Backup model after primary failures, `liquid/lfm-2.5-2.6b:free` by default. |
   | `KNOWLEDGE_FILE` | no | Knowledge base path, `knowledge/dzala.md` by default. |
   | `AI_SESSION_IDLE_TIMEOUT` | no | AI/unassigned-session inactivity timeout, `24h` by default. |
   | `OPERATOR_SESSION_IDLE_TIMEOUT` | no | Taken operator-dialog inactivity timeout, `48h` by default. |
   | `SESSION_SWEEP_INTERVAL` | no | Inactivity scan interval, `10m` by default. |
   | `AI_MIN_QUESTION_INTERVAL` | no | Per-chat delay between AI questions, `5s` by default. |
   | `AI_MAX_QUESTIONS_PER_SESSION` | no | AI question cap per session, `30` by default. |

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
without provider restrictions. If both primary attempts fail, the same two attempts
are made with `OPENROUTER_FALLBACK_MODEL`. Failed attempts log only the model name
and a safe error summary; API keys and client data are never logged.

## Operator group

1. Create a private Telegram group for the operators and add the bot to it.
2. Send `/chatid` in the group; the bot replies with the group ID.
3. Put that ID into `TELEGRAM_MANAGER_CHAT_ID` and restart the bot.

## Client flow

1. `/start` introduces «ИИ-помощник Dzala» and shows «Тренировки», «Кэмпы»,
   «О Dzala» and «Связаться с оператором». `/start`, `/help` and `/exit` are
   registered in Telegram's slash-command menu. A plain text message sent before
   `/start` starts a session, shows the menu and is then processed as the first
   question.
2. «Тренировки» opens topic buttons for tennis, padel, children, adults, coaches,
   prices and locations. Topic cards keep back-to-training, operator and main-menu
   navigation available.
3. «Кэмпы» opens the three destinations. Choosing one immediately shows its short
   card and topic buttons for program, dates and price, accommodation, coaches and
   practical information. Every camp card has back-to-camps navigation.
4. Any plain text message is treated as a question. While OpenRouter is working,
   Telegram shows a typing action. Only one AI request may run per chat; later
   questions are throttled and stale responses are discarded after `/start` or
   `/exit`. A session permits at most the configured number of AI questions. The
   request contains the knowledge base, active section, topic, camp, bounded
   history and current question, and expects `answer`, `clarify` or `handoff`.
5. `answer` and `clarify` replies carry contextual training or camp actions.
   Questions about booking, availability, payment, discounts, refunds,
   cancellation, visas, flights, medical limitations or individual conditions go
   to an operator without an AI call.
6. The first AI question creates one informational card in the private operator
   group. A later handoff converts that same card into a ticket instead of posting
   a duplicate. The ticket includes section, topic, camp, question and transcript.
   AI failures and invalid responses trigger immediate handoff.
7. `/exit`, a new `/start`, operator closure or inactivity ends the session, updates
   its operator card and posts the final transcript. Untaken/AI sessions expire
   after 24 hours by default; taken operator dialogs expire after 48 hours.
8. `/stats` in the configured operator group reports aggregate counters only:
   sessions, AI success/failure, handoffs, timeouts and throttling. It contains no
   client identity or message text.

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

- Dialog, timers, audit cards and ticket state are kept in memory only, so they
  are lost on restart. Idle closure runs only while the bot process is awake.
- Only the last 20 session messages are sent to the AI; operators still receive
  the complete session transcript.
- Production will use a webhook in a later development stage; only polling is
  implemented. A continuously running host is required for reliable timeouts.

## Checks

```bash
go fmt ./...
go vet ./...
go test ./...
```
