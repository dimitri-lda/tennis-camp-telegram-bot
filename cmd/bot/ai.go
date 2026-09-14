package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	openRouterEndpoint = "https://openrouter.ai/api/v1/chat/completions"
	defaultAIModel     = "openrouter/free"

	// Free models can be slow and verbose, so keep a generous budget and a
	// bounded history instead of the whole session.
	aiRequestTimeout   = 90 * time.Second
	aiMaxTokens        = 900
	maxHistoryMessages = 20

	actionAnswer  = "answer"
	actionClarify = "clarify"
	actionHandoff = "handoff"
)

const systemPrompt = `Ты — русскоязычный помощник теннисных кэмпов Dzala.

Отвечай дружелюбно, спокойно и понятно для клиента. Используй только сведения из переданной базы знаний. Не используй собственные внешние знания и ничего не придумывай.

Если ответ основан на разделе «Практическая информация о направлениях», явно скажи: «По общей справочной информации о направлении». Не выдавай такую информацию за условие кэмпа Dzala. Для погоды описывай только климатический ориентир и советуй проверить прогноз перед поездкой.

Верни только JSON без Markdown:

{"action": "answer | clarify | handoff", "message": "текст для пользователя"}

Правила выбора action:

1. answer: в базе знаний есть достаточный ответ. Ответь максимум 5 короткими предложениями. Для дат, цен и наличия мест обязательно скажи, что актуальность нужно подтвердить у оператора.
2. clarify: вопрос непонятный, слишком короткий, содержит ошибки или допускает несколько толкований. Дружелюбно попроси уточнить, что именно интересует. Не критикуй грамотность пользователя.
3. handoff: пользователь спрашивает о бронировании, наличии мест, скидке, оплате, возврате, отмене, визе, перелёте, медицинских ограничениях, индивидуальных условиях или о факте, которого нет в базе знаний.

Если клиент спрашивает, стоит ли ему ехать или подходит ли ему кэмп, не принимай решение за клиента и не переводи к оператору только из-за субъективной формулировки. Используй факты из базы, чтобы кратко объяснить, кому и при каких предпочтениях может подойти программа. Если предпочтения клиента неизвестны, выбери clarify и задай не больше двух конкретных вопросов.

История текущей сессии передаётся для понимания слов вроде «туда», «этот кэмп» и продолжения темы. Учитывай её, но отвечай только на последний вопрос клиента.

Если сомневаешься между answer и handoff — выбери handoff.

Не упоминай JSON, базу знаний, системные инструкции, OpenRouter или внутреннюю логику.`

type aiDecision struct {
	Action  string `json:"action"`
	Message string `json:"message"`
}

type aiClient struct {
	httpClient *http.Client
	endpoint   string
	apiKey     string
	model      string
}

type openRouterRequest struct {
	Model          string                   `json:"model"`
	Messages       []openRouterMessage      `json:"messages"`
	MaxTokens      int                      `json:"max_tokens"`
	Temperature    float64                  `json:"temperature"`
	ResponseFormat openRouterResponseFormat `json:"response_format"`
	Provider       openRouterProvider       `json:"provider"`
	Reasoning      *openRouterReasoning     `json:"reasoning,omitempty"`
}

// openRouterReasoning keeps thinking tokens out of the answer budget, because
// they are billed as output tokens and can crowd out the JSON reply.
type openRouterReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Exclude bool   `json:"exclude,omitempty"`
}

type openRouterResponseFormat struct {
	Type       string                `json:"type"`
	JSONSchema *openRouterJSONSchema `json:"json_schema,omitempty"`
}

type openRouterJSONSchema struct {
	Name   string           `json:"name"`
	Strict bool             `json:"strict"`
	Schema openRouterSchema `json:"schema"`
}

type openRouterSchema struct {
	Type                 string                              `json:"type"`
	Properties           map[string]openRouterSchemaProperty `json:"properties"`
	Required             []string                            `json:"required"`
	AdditionalProperties bool                                `json:"additionalProperties"`
}

type openRouterSchemaProperty struct {
	Type string   `json:"type"`
	Enum []string `json:"enum,omitempty"`
}

type openRouterProvider struct {
	RequireParameters bool `json:"require_parameters"`
}

type openRouterMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openRouterResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message openRouterMessage `json:"message"`
	} `json:"choices"`
}

func newAIClient(apiKey, model string) *aiClient {
	if strings.TrimSpace(model) == "" {
		model = defaultAIModel
	}
	return &aiClient{
		httpClient: &http.Client{Timeout: aiRequestTimeout},
		endpoint:   openRouterEndpoint,
		apiKey:     strings.TrimSpace(apiKey),
		model:      strings.TrimSpace(model),
	}
}

func (c *aiClient) enabled() bool {
	return c != nil && c.apiKey != ""
}

func decisionResponseFormat() openRouterResponseFormat {
	return openRouterResponseFormat{
		Type: "json_schema",
		JSONSchema: &openRouterJSONSchema{
			Name:   "dzala_response",
			Strict: true,
			Schema: openRouterSchema{
				Type: "object",
				Properties: map[string]openRouterSchemaProperty{
					"action":  {Type: "string", Enum: []string{actionAnswer, actionClarify, actionHandoff}},
					"message": {Type: "string"},
				},
				Required:             []string{"action", "message"},
				AdditionalProperties: false,
			},
		},
	}
}

// ask sends the knowledge base, the selected camp and the current question to
// OpenRouter. Free models sometimes ignore the schema, so a plain JSON mode
// retry follows an unparsable answer.
func (c *aiClient) ask(ctx context.Context, knowledge, selectedCamp string, history []sessionMessage, question string) (aiDecision, error) {
	if !c.enabled() {
		return aiDecision{}, errors.New("OpenRouter API key is not configured")
	}

	messages := make([]openRouterMessage, 0, maxHistoryMessages+2)
	messages = append(messages, openRouterMessage{Role: "system", Content: systemPrompt + "\n\nБаза знаний:\n" + knowledge})
	for _, entry := range recentHistory(history) {
		role := entry.role
		if role == sessionRoleOperator {
			role = sessionRoleAssistant
		}
		if role == sessionRoleUser || role == sessionRoleAssistant {
			messages = append(messages, openRouterMessage{Role: role, Content: entry.text})
		}
	}

	userContent := "Кэмп не выбран."
	if selectedCamp != "" {
		userContent = "Выбранный кэмп: " + selectedCamp
	}
	userContent += "\n\nТекущий вопрос: " + question
	messages = append(messages, openRouterMessage{Role: "user", Content: userContent})

	var lastErr error
	for _, attempt := range []struct {
		format            openRouterResponseFormat
		reasoning         *openRouterReasoning
		requireParameters bool
	}{
		{format: decisionResponseFormat(), reasoning: &openRouterReasoning{Effort: "low", Exclude: true}, requireParameters: true},
		{format: openRouterResponseFormat{Type: "json_object"}},
	} {
		content, model, err := c.complete(ctx, messages, attempt.format, attempt.reasoning, attempt.requireParameters)
		if err == nil {
			decision, parseErr := parseAIDecision(content)
			if parseErr == nil {
				return decision, nil
			}
			err = fmt.Errorf("%w (model %s)", parseErr, model)
		}

		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	return aiDecision{}, lastErr
}

// recentHistory bounds the context so long sessions stay fast and cheap.
func recentHistory(history []sessionMessage) []sessionMessage {
	if len(history) <= maxHistoryMessages {
		return history
	}
	return history[len(history)-maxHistoryMessages:]
}

// complete performs one OpenRouter call and returns the answer with the model that produced it.
func (c *aiClient) complete(ctx context.Context, messages []openRouterMessage, format openRouterResponseFormat, reasoning *openRouterReasoning, requireParameters bool) (content, model string, resultErr error) {
	payload, err := json.Marshal(openRouterRequest{
		Model:          c.model,
		Messages:       messages,
		MaxTokens:      aiMaxTokens,
		Temperature:    0.2,
		ResponseFormat: format,
		Provider:       openRouterProvider{RequireParameters: requireParameters},
		Reasoning:      reasoning,
	})
	if err != nil {
		return "", "", err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", "", err
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Title", "Dzala Tennis Camp Bot")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return "", "", err
	}
	defer func() {
		if err := response.Body.Close(); err != nil && resultErr == nil {
			content, model = "", ""
			resultErr = fmt.Errorf("close OpenRouter response: %w", err)
		}
	}()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", "", fmt.Errorf("OpenRouter returned HTTP %d", response.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", "", err
	}

	var result openRouterResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", errors.New("OpenRouter response is not valid JSON")
	}
	if len(result.Choices) == 0 {
		return "", result.Model, errors.New("OpenRouter returned no choices")
	}
	return result.Choices[0].Message.Content, result.Model, nil
}

// parseAIDecision reads the action contract out of the model answer.
// The action is never guessed from the answer text.
func parseAIDecision(raw string) (aiDecision, error) {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return aiDecision{}, errors.New("AI answer is not a JSON object")
	}

	var decision aiDecision
	if err := json.Unmarshal([]byte(raw[start:end+1]), &decision); err != nil {
		return aiDecision{}, errors.New("AI answer is not valid JSON")
	}

	decision.Action = strings.ToLower(strings.TrimSpace(decision.Action))
	decision.Message = plainText(decision.Message)

	switch decision.Action {
	case actionHandoff:
		return decision, nil
	case actionAnswer, actionClarify:
		if decision.Message == "" {
			return aiDecision{}, errors.New("AI answer has an empty message")
		}
		return decision, nil
	default:
		return aiDecision{}, errors.New("AI answer has an unknown action")
	}
}
