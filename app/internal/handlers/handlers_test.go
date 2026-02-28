// Package handlers tests — uses package-level access to test unexported helpers.
package handlers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"clearoutspaces/internal/config"
	"clearoutspaces/internal/database"
	"clearoutspaces/internal/llm"
)

// ─── Test helpers ─────────────────────────────────────────────────────────────

func testConfig() *config.Config {
	return &config.Config{
		DBPath:             ":memory:",
		MetaVerifyToken:    "test-verify-token",
		MetaAppSecret:      "test-app-secret",
		MetaAccessToken:    "test-access-token",
		MetaPhoneNumberID:  "123456789",
		DeepSeekAPIKey:     "test-deepseek-key",
		SlackWebhookURL:    "https://hooks.slack.com/test",
		SlackSigningSecret: "test-slack-secret",
	}
}

func testDB(t *testing.T) *database.DB {
	t.Helper()
	db := database.Init(":memory:")
	t.Cleanup(func() {})
	return db
}

func metaSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return fmt.Sprintf("sha256=%x", mac.Sum(nil))
}

func slackSignature(secret, timestamp string, body []byte) string {
	base := fmt.Sprintf("v0:%s:%s", timestamp, string(body))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(base))
	return fmt.Sprintf("v0=%x", mac.Sum(nil))
}

// ─── GET /health ──────────────────────────────────────────────────────────────

func TestHealthCheck(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	HealthCheck(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if body["status"] != "healthy" {
		t.Errorf("expected status=healthy, got %q", body["status"])
	}
}

// ─── GET /whatsapp/webhook (verification) ────────────────────────────────────

func TestVerifyWebhook_Valid(t *testing.T) {
	cfg := testConfig()
	handler := VerifyWebhook(cfg)

	req := httptest.NewRequest(http.MethodGet, "/whatsapp/webhook?hub.mode=subscribe&hub.challenge=abc123&hub.verify_token=test-verify-token", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != "abc123" {
		t.Errorf("expected challenge abc123, got %q", w.Body.String())
	}
}

func TestVerifyWebhook_WrongToken(t *testing.T) {
	cfg := testConfig()
	handler := VerifyWebhook(cfg)

	req := httptest.NewRequest(http.MethodGet, "/whatsapp/webhook?hub.mode=subscribe&hub.challenge=abc123&hub.verify_token=WRONG", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestVerifyWebhook_WrongMode(t *testing.T) {
	cfg := testConfig()
	handler := VerifyWebhook(cfg)

	req := httptest.NewRequest(http.MethodGet, "/whatsapp/webhook?hub.mode=unsubscribe&hub.challenge=abc123&hub.verify_token=test-verify-token", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

// ─── HMAC signature verification ─────────────────────────────────────────────

func TestVerifyMetaSignature_Valid(t *testing.T) {
	body := []byte(`{"test":"payload"}`)
	sig := metaSignature("my-secret", body)

	if !verifyMetaSignature("my-secret", body, sig) {
		t.Error("expected valid signature to pass")
	}
}

func TestVerifyMetaSignature_Invalid(t *testing.T) {
	body := []byte(`{"test":"payload"}`)
	if verifyMetaSignature("my-secret", body, "sha256=badhash") {
		t.Error("expected bad signature to fail")
	}
}

func TestVerifyMetaSignature_Empty(t *testing.T) {
	if verifyMetaSignature("my-secret", []byte("body"), "") {
		t.Error("expected empty signature to fail")
	}
}

func TestVerifyMetaSignature_BodyTampered(t *testing.T) {
	body := []byte(`{"test":"payload"}`)
	sig := metaSignature("my-secret", body)
	tampered := []byte(`{"test":"TAMPERED"}`)

	if verifyMetaSignature("my-secret", tampered, sig) {
		t.Error("expected tampered body to fail verification")
	}
}

// ─── POST /whatsapp/webhook (inbound message) ─────────────────────────────────

func TestHandleWhatsAppMessage_BadSignature_Returns403(t *testing.T) {
	cfg := testConfig()
	db := testDB(t)
	handler := HandleWhatsAppMessage(db, cfg)

	body := []byte(`{"object":"whatsapp_business_account"}`)
	req := httptest.NewRequest(http.MethodPost, "/whatsapp/webhook", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256=badsignature")

	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for bad signature, got %d", w.Code)
	}
}

func TestHandleWhatsAppMessage_MissingSignature_Returns403(t *testing.T) {
	cfg := testConfig()
	db := testDB(t)
	handler := HandleWhatsAppMessage(db, cfg)

	body := []byte(`{"object":"whatsapp_business_account"}`)
	req := httptest.NewRequest(http.MethodPost, "/whatsapp/webhook", bytes.NewReader(body))
	// No signature header.

	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for missing signature, got %d", w.Code)
	}
}

func TestHandleWhatsAppMessage_ValidSignature_Returns200(t *testing.T) {
	cfg := testConfig()
	db := testDB(t)

	// Mock Meta send API — accept any POST and return 200.
	fakeMetaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"messages":[{"id":"wamid.ok"}]}`))
	}))
	defer fakeMetaServer.Close()
	metaAPIBaseURL = fakeMetaServer.URL

	// Mock DeepSeek — return a valid JSON response.
	fakeDeepSeek := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		resp := `{"choices":[{"message":{"content":"{\"reply_to_user\":\"Hi! What's the address?\",\"extracted_data\":{\"address\":\"unknown\",\"elevator_access\":\"unknown\",\"stairs\":\"unknown\",\"inventory\":\"1 couch\"},\"action\":\"continue\"}"}}]}`
		w.Write([]byte(resp))
	}))
	defer fakeDeepSeek.Close()
	llm.SetBaseURL(fakeDeepSeek.URL + "/chat/completions")

	// Load a dummy prompt so llm.SystemPrompt() isn't empty.
	llm.SetSystemPromptForTest("You are a test assistant.")

	handler := HandleWhatsAppMessage(db, cfg)

	payload := `{"object":"whatsapp_business_account","entry":[{"changes":[{"value":{"messages":[{"from":"14165551234","id":"wamid.test001","type":"text","text":{"body":"I need a couch removed."}}]}}]}]}`
	body := []byte(payload)
	sig := metaSignature(cfg.MetaAppSecret, body)

	req := httptest.NewRequest(http.MethodPost, "/whatsapp/webhook", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sig)

	w := httptest.NewRecorder()
	handler(w, req)

	// Must return 200 immediately regardless of async work.
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	// Give the goroutine time to write to DB.
	time.Sleep(300 * time.Millisecond)

	// Verify the message was saved.
	exists, err := db.MessageExists("wamid.test001")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("expected user message to be saved in DB after processing")
	}
}

func TestHandleWhatsAppMessage_StatusPayload_Returns200(t *testing.T) {
	// Meta sends delivery receipts with no messages array. Must not crash.
	cfg := testConfig()
	db := testDB(t)
	handler := HandleWhatsAppMessage(db, cfg)

	body := []byte(`{"object":"whatsapp_business_account","entry":[{"changes":[{"value":{"statuses":[{"id":"wamid.status","status":"delivered"}]}}]}]}`)
	sig := metaSignature(cfg.MetaAppSecret, body)

	req := httptest.NewRequest(http.MethodPost, "/whatsapp/webhook", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sig)

	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for status payload, got %d", w.Code)
	}
}

// ─── Slack signature verification ─────────────────────────────────────────────

func TestVerifySlackSignature_Valid(t *testing.T) {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	body := []byte("payload=test")
	sig := slackSignature("test-slack-secret", timestamp, body)

	if !verifySlackSignature("test-slack-secret", timestamp, body, sig) {
		t.Error("expected valid Slack signature to pass")
	}
}

func TestVerifySlackSignature_Invalid(t *testing.T) {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	body := []byte("payload=test")

	if verifySlackSignature("test-slack-secret", timestamp, body, "v0=badsig") {
		t.Error("expected invalid sig to fail")
	}
}

func TestVerifySlackSignature_ReplayAttack(t *testing.T) {
	// Timestamp older than 5 minutes.
	oldTimestamp := strconv.FormatInt(time.Now().Unix()-400, 10)
	body := []byte("payload=test")
	sig := slackSignature("test-slack-secret", oldTimestamp, body)

	if verifySlackSignature("test-slack-secret", oldTimestamp, body, sig) {
		t.Error("expected old timestamp to fail (replay attack prevention)")
	}
}

// ─── POST /slack/interactive ─────────────────────────────────────────────────

func TestHandleSlackInteractive_BadSignature_Returns403(t *testing.T) {
	cfg := testConfig()
	db := testDB(t)
	handler := HandleSlackInteractive(db, cfg)

	formBody := url.Values{}
	formBody.Set("payload", `{"type":"block_actions","actions":[]}`)
	body := []byte(formBody.Encode())

	req := httptest.NewRequest(http.MethodPost, "/slack/interactive", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Slack-Request-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	req.Header.Set("X-Slack-Signature", "v0=badsignature")

	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestHandleSlackInteractive_TakeOver_PausesConversation(t *testing.T) {
	cfg := testConfig()
	db := testDB(t)

	// Set up an active conversation.
	if err := db.UpsertConversation("14165551234"); err != nil {
		t.Fatal(err)
	}

	handler := HandleSlackInteractive(db, cfg)

	slackPayload := `{"type":"block_actions","user":{"id":"U123","username":"adriantest"},"actions":[{"action_id":"take_over_chat","value":"14165551234"}]}`
	formBody := url.Values{}
	formBody.Set("payload", slackPayload)
	body := []byte(formBody.Encode())

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	sig := slackSignature(cfg.SlackSigningSecret, timestamp, body)

	req := httptest.NewRequest(http.MethodPost, "/slack/interactive", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Slack-Request-Timestamp", timestamp)
	req.Header.Set("X-Slack-Signature", sig)

	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	// Verify the conversation is now PAUSED.
	status, err := db.GetConversationStatus("14165551234")
	if err != nil {
		t.Fatal(err)
	}
	if status != "PAUSED" {
		t.Errorf("expected conversation to be PAUSED, got %s", status)
	}

	// Verify response body tells Slack the correct message.
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["replace_original"] != true {
		t.Error("expected replace_original=true in Slack response")
	}
	if !strings.Contains(fmt.Sprintf("%v", resp["text"]), "adriantest") {
		t.Errorf("expected username in Slack response, got: %v", resp["text"])
	}
}

func TestHandleSlackInteractive_UnknownConversation_Returns200WithWarning(t *testing.T) {
	cfg := testConfig()
	db := testDB(t)
	handler := HandleSlackInteractive(db, cfg)

	slackPayload := `{"type":"block_actions","user":{"id":"U123","username":"adriantest"},"actions":[{"action_id":"take_over_chat","value":"99999999999"}]}`
	formBody := url.Values{}
	formBody.Set("payload", slackPayload)
	body := []byte(formBody.Encode())

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	sig := slackSignature(cfg.SlackSigningSecret, timestamp, body)

	req := httptest.NewRequest(http.MethodPost, "/slack/interactive", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Slack-Request-Timestamp", timestamp)
	req.Header.Set("X-Slack-Signature", sig)

	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 even for unknown conversation, got %d", w.Code)
	}
}

func TestHandleSlackInteractive_AlreadyPaused(t *testing.T) {
	cfg := testConfig()
	db := testDB(t)

	if err := db.UpsertConversation("14165551234"); err != nil {
		t.Fatal(err)
	}
	if err := db.PauseConversation("14165551234"); err != nil {
		t.Fatal(err)
	}

	handler := HandleSlackInteractive(db, cfg)

	slackPayload := `{"type":"block_actions","user":{"id":"U123","username":"adriantest"},"actions":[{"action_id":"take_over_chat","value":"14165551234"}]}`
	formBody := url.Values{}
	formBody.Set("payload", slackPayload)
	body := []byte(formBody.Encode())

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	sig := slackSignature(cfg.SlackSigningSecret, timestamp, body)

	req := httptest.NewRequest(http.MethodPost, "/slack/interactive", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Slack-Request-Timestamp", timestamp)
	req.Header.Set("X-Slack-Signature", sig)

	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if !strings.Contains(fmt.Sprintf("%v", resp["text"]), "already paused") {
		t.Errorf("expected already-paused message, got: %v", resp["text"])
	}
}
