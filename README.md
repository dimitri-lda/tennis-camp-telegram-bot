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
   | `OPENROUTER_MODEL` | no | Model name, `openrouter/free` by default. Prefer a fixed model, see below. |
   | `KNOWLEDGE_FILE` | no | Knowledge base path, `knowledge/dzala.md` by default. |

3. Run the bot in polling mode from the repository root, so that the default
   knowledge base path resolves:

   ```bash
   go run ./cmd/bot
   ```

The knowledge base is read once at startup. The bot refuses to start when the
file is missing or empty. It contains both confirmed Dzala camp materials and a
separately labelled practical destination FAQ based on public sources. The bot
must present the latter as general reference information, not a Dzala condition.

## Choosing a model

`openrouter/free` picks a random free model per request, so answer quality,
latency and schema compliance vary. Set a fixed model for predictable answers:

```env
OPENROUTER_MODEL=deepseek/deepseek-v4-flash-0731
```

Other inexpensive options with reliable structured outputs are `z-ai/glm-4.7-flash`,
`qwen/qwen3.5-flash-02-23` and `google/gemini-2.5-flash-lite`. Avoid `~vendor/model-latest`
aliases: some of them do not support structured outputs. Paid models require credits
on the OpenRouter account; a ChatGPT subscription does not cover API usage.

The first attempt asks for a strict JSON schema and caps reasoning, because
thinking tokens are billed as output tokens and can crowd out the JSON answer. An
unparsable answer triggers one retry in plain JSON mode without provider
restrictions. Failed attempts log the model that answered, so
`ask OpenRouter: ... (model vendor/name)` identifies a misbehaving model.

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
