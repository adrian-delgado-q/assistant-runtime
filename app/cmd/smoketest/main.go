// smoketest verifies live connectivity for the Slack webhook and the local API.
// Run with: go run ./cmd/smoketest/main.go
// Reads the same env vars as the main server (source .env.dev first).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const localAPI = "http://localhost:8080"

func main() {
	passed := 0
	failed := 0

	run := func(name string, fn func() error) {
		fmt.Printf("  %-45s", name)
		if err := fn(); err != nil {
			fmt.Printf("FAIL — %v\n", err)
			failed++
		} else {
			fmt.Printf("OK\n")
			passed++
		}
	}

	fmt.Println("\n── Local API ───────────────────────────────────────────────")
	run("GET /health returns 200 + {status:healthy}", checkHealth)

	fmt.Println("\n── Webhook verification ────────────────────────────────────")
	run("GET /whatsapp/webhook with correct token", checkWebhookVerify)
	run("GET /whatsapp/webhook with wrong token returns 403", checkWebhookWrongToken)

	fmt.Println("\n── Slack connectivity ──────────────────────────────────────")
	run("POST to SLACK_WEBHOOK_URL sends test message", checkSlackWebhook)

	fmt.Printf("\n%d passed, %d failed\n\n", passed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}

func checkHealth() error {
	resp, err := get(localAPI + "/health")
	if err != nil {
		return fmt.Errorf("could not reach server (is it running?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if body["status"] != "healthy" {
		return fmt.Errorf("expected status=healthy, got %q", body["status"])
	}
	return nil
}

func checkWebhookVerify() error {
	token := requireEnv("META_VERIFY_TOKEN")
	url := fmt.Sprintf("%s/whatsapp/webhook?hub.mode=subscribe&hub.challenge=ping&hub.verify_token=%s", localAPI, token)
	resp, err := get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("expected 200, got %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "ping" {
		return fmt.Errorf("expected challenge=ping, got %q", string(b))
	}
	return nil
}

func checkWebhookWrongToken() error {
	url := fmt.Sprintf("%s/whatsapp/webhook?hub.mode=subscribe&hub.challenge=ping&hub.verify_token=WRONG", localAPI)
	resp, err := get(url)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		return fmt.Errorf("expected 403, got %d", resp.StatusCode)
	}
	return nil
}

func checkSlackWebhook() error {
	webhookURL := requireEnv("SLACK_WEBHOOK_URL")

	payload := map[string]interface{}{
		"text": "🔧 *ClearoutSpaces smoke test* — Slack connectivity confirmed. You can ignore this message.",
		"blocks": []interface{}{
			map[string]interface{}{
				"type": "section",
				"text": map[string]interface{}{
					"type": "mrkdwn",
					"text": fmt.Sprintf("*Smoke test passed* at `%s`\nSlack webhook is connected to the ClearoutSpaces API.", time.Now().Format(time.RFC3339)),
				},
			},
		},
	}
	b, _ := json.Marshal(payload)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST to Slack webhook failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Slack returned %d: %s", resp.StatusCode, string(body))
	}
	if string(body) != "ok" {
		return fmt.Errorf("Slack returned unexpected body: %q", string(body))
	}
	return nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func get(url string) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Printf("\n  WARN: %s is not set — test will fail\n", key)
	}
	return v
}
