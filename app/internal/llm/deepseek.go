package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"clearoutspaces/internal/models"
)

// deepSeekURL is a var so tests can override it with an httptest.Server URL.
var deepSeekURL = "https://api.deepseek.com/chat/completions"

const (
	deepSeekModel = "deepseek-chat"
	httpTimeout   = 30 * time.Second
)

var httpClient = &http.Client{Timeout: httpTimeout}

type deepSeekRequest struct {
	Model          string              `json:"model"`
	Messages       []models.LLMMessage `json:"messages"`
	ResponseFormat map[string]string   `json:"response_format"`
}

type deepSeekResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Call sends the conversation history to DeepSeek and returns a validated LLMResponse.
// Falls back gracefully on LLM errors — never returns a nil LLMResponse when err == nil.
func Call(ctx context.Context, apiKey string, history []models.Message) (*models.LLMResponse, error) {
	msgs := []models.LLMMessage{
		{Role: "system", Content: SystemPrompt()},
	}
	for _, m := range history {
		msgs = append(msgs, models.LLMMessage{Role: m.Role, Content: m.Content})
	}

	reqBody, err := json.Marshal(deepSeekRequest{
		Model:          deepSeekModel,
		Messages:       msgs,
		ResponseFormat: map[string]string{"type": "json_object"},
	})
	if err != nil {
		return fallback(), fmt.Errorf("llm: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deepSeekURL, bytes.NewReader(reqBody))
	if err != nil {
		return fallback(), fmt.Errorf("llm: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return fallback(), fmt.Errorf("llm: http call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fallback(), fmt.Errorf("llm: unexpected status %d", resp.StatusCode)
	}

	var dsResp deepSeekResponse
	if err := json.NewDecoder(resp.Body).Decode(&dsResp); err != nil {
		return fallback(), fmt.Errorf("llm: decode response: %w", err)
	}
	if len(dsResp.Choices) == 0 {
		return fallback(), fmt.Errorf("llm: empty choices")
	}

	var llmResp models.LLMResponse
	if err := json.Unmarshal([]byte(dsResp.Choices[0].Message.Content), &llmResp); err != nil {
		return fallback(), fmt.Errorf("llm: parse JSON content: %w", err)
	}

	// Validate required fields.
	if llmResp.ReplyToUser == "" {
		llmResp.ReplyToUser = "I'm looking into that, one moment!"
	}
	if !validAction(llmResp.Action) {
		llmResp.Action = "continue"
	}

	return &llmResp, nil
}

func validAction(a string) bool {
	return a == "continue" || a == "handoff" || a == "schedule"
}

// fallback returns a safe default response used when the LLM call fails entirely.
func fallback() *models.LLMResponse {
	return &models.LLMResponse{
		ReplyToUser: "Sorry, I ran into a technical issue. Our team will follow up with you shortly.",
		Action:      "continue",
	}
}

// ─── Test helpers (exported for use in handler tests) ─────────────────────────

// SetBaseURL overrides deepSeekURL. Only call this from tests.
func SetBaseURL(url string) {
	deepSeekURL = url
}
