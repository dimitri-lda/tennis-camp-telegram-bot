package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseAIDecision(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		wantAction  string
		wantMessage string
	}{
		{
			name:        "answer",
			raw:         `{"action":"answer","message":"В день от 3 до 5 часов тенниса."}`,
			wantAction:  actionAnswer,
			wantMessage: "В день от 3 до 5 часов тенниса.",
		},
		{
			name:        "clarify wrapped in a code fence",
			raw:         "```json\n{\"action\": \"CLARIFY\", \"message\": \"Уточните, какой кэмп интересует?\"}\n```",
			wantAction:  actionClarify,
			wantMessage: "Уточните, какой кэмп интересует?",
		},
		{
			name:       "handoff without a message",
			raw:        `{"action":"handoff","message":""}`,
			wantAction: actionHandoff,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision, err := parseAIDecision(test.raw)
			if err != nil {
				t.Fatalf("parseAIDecision() error = %v", err)
			}
			if decision.Action != test.wantAction {
				t.Errorf("action = %q, want %q", decision.Action, test.wantAction)
			}
			if decision.Message != test.wantMessage {
				t.Errorf("message = %q, want %q", decision.Message, test.wantMessage)
			}
		})
	}
}

func TestParseAIDecisionRejectsBadAnswers(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "plain text", raw: "Конечно, расскажу про кэмп!"},
		{name: "broken JSON", raw: `{"action":"answer","message":}`},
		{name: "unknown action", raw: `{"action":"escalate","message":"текст"}`},
		{name: "empty answer message", raw: `{"action":"answer","message":"   "}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseAIDecision(test.raw); err == nil {
				t.Error("parseAIDecision() error = nil, want an error")
			}
		})
	}
}

func TestAskSendsKnowledgeCampPreviousTurnAndSchema(t *testing.T) {
	var request openRouterRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, reader *http.Request) {
		body, err := io.ReadAll(reader.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"choices":[{"message":{"content":"{\"action\":\"answer\",\"message\":\"От 3 до 5 часов.\"}"}}]}`)
	}))
	defer server.Close()

	client := newAIClient("test-key", "vendor/test-model")
	client.endpoint = server.URL

	history := []sessionMessage{
		{role: sessionRoleUser, text: "Что такое кэмп в Тбилиси?"},
		{role: sessionRoleAssistant, text: "Это теннисный кэмп с тренировками и проживанием."},
		{role: sessionRoleUser, text: "А какие там тренировки?"},
		{role: sessionRoleAssistant, text: "От 3 до 5 часов тенниса ежедневно."},
	}
	decision, err := client.ask(context.Background(), "База знаний про кэмпы", "Тбилиси, Грузия", history, "Стоит ли мне туда ехать?")
	if err != nil {
		t.Fatalf("ask() error = %v", err)
	}
	if decision.Action != actionAnswer || decision.Message != "От 3 до 5 часов." {
		t.Errorf("ask() = %+v, want an answer decision", decision)
	}
	if request.Model != "vendor/test-model" {
		t.Errorf("model = %q, want %q", request.Model, "vendor/test-model")
	}
	if len(request.Messages) != 6 {
		t.Fatalf("messages = %d, want system, four history messages and current question", len(request.Messages))
	}
	if !strings.Contains(request.Messages[0].Content, "База знаний про кэмпы") {
		t.Error("system message must carry the knowledge base")
	}
	for index, want := range []openRouterMessage{
		{Role: "user", Content: "Что такое кэмп в Тбилиси?"},
		{Role: "assistant", Content: "Это теннисный кэмп с тренировками и проживанием."},
		{Role: "user", Content: "А какие там тренировки?"},
		{Role: "assistant", Content: "От 3 до 5 часов тенниса ежедневно."},
	} {
		if request.Messages[index+1] != want {
			t.Errorf("history message %d = %+v, want %+v", index, request.Messages[index+1], want)
		}
	}
	if !strings.Contains(request.Messages[5].Content, "Тбилиси, Грузия") || !strings.Contains(request.Messages[5].Content, "Стоит ли мне туда ехать?") {
		t.Errorf("current user message = %q, want camp and current question", request.Messages[5].Content)
	}
	if request.ResponseFormat.Type != "json_schema" || !request.ResponseFormat.JSONSchema.Strict {
		t.Errorf("response format = %+v, want strict JSON schema", request.ResponseFormat)
	}
	if !request.Provider.RequireParameters {
		t.Error("provider.require_parameters = false, want true")
	}
	action := request.ResponseFormat.JSONSchema.Schema.Properties["action"]
	if strings.Join(action.Enum, ",") != "answer,clarify,handoff" {
		t.Errorf("action enum = %v, want answer, clarify, handoff", action.Enum)
	}
}

func TestAskFailsOnServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := newAIClient("test-key", "")
	client.endpoint = server.URL

	if _, err := client.ask(context.Background(), "knowledge", "", nil, "вопрос"); err == nil {
		t.Error("ask() error = nil, want an error for HTTP 500")
	}
}

func TestAIClientWithoutAPIKeyIsDisabled(t *testing.T) {
	client := newAIClient("  ", "")
	if client.enabled() {
		t.Error("enabled() = true, want false without OPENROUTER_API_KEY")
	}
	if client.model != defaultAIModel {
		t.Errorf("model = %q, want the default %q", client.model, defaultAIModel)
	}
	if _, err := client.ask(context.Background(), "knowledge", "", nil, "вопрос"); err == nil {
		t.Error("ask() error = nil, want an error without OPENROUTER_API_KEY")
	}
}
