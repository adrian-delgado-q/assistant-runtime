package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"clearoutspaces/internal/config"
	"clearoutspaces/internal/database"
	"clearoutspaces/internal/llm"
	"clearoutspaces/internal/models"
)

// metaAPIBaseURL is a var so tests can override it with an httptest.Server URL.
var metaAPIBaseURL = "https://graph.facebook.com"

// conversationLocks serialises processing per phone number to prevent race
// conditions when a user sends multiple messages in quick succession.
var (
	conversationLocks sync.Map // map[phoneNumber] -> *sync.Mutex
)

func lockFor(phone string) *sync.Mutex {
	v, _ := conversationLocks.LoadOrStore(phone, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// ─── GET /whatsapp/webhook ────────────────────────────────────────────────────

func VerifyWebhook(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mode := r.URL.Query().Get("hub.mode")
		challenge := r.URL.Query().Get("hub.challenge")
		token := r.URL.Query().Get("hub.verify_token")

		if mode == "subscribe" && token == cfg.MetaVerifyToken {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, challenge)
			return
		}
		http.Error(w, "forbidden", http.StatusForbidden)
	}
}

// ─── POST /whatsapp/webhook ───────────────────────────────────────────────────

func HandleWhatsAppMessage(db *database.DB, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1. Read raw body first — required for HMAC verification.
		rawBody, err := io.ReadAll(r.Body)
		if err != nil {
			log.Printf("whatsapp: failed to read body: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// 2. Verify HMAC-SHA256 signature.
		if !verifyMetaSignature(cfg.MetaAppSecret, rawBody, r.Header.Get("X-Hub-Signature-256")) {
			log.Println("whatsapp: invalid signature")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		// 3. Return 200 immediately — Meta requires a fast ack.
		w.WriteHeader(http.StatusOK)

		// 4. Process asynchronously.
		go func() {
			defer func() {
				if rec := recover(); rec != nil {
					log.Printf("whatsapp: recovered from panic: %v", rec)
				}
			}()
			processInbound(db, cfg, rawBody)
		}()
	}
}

func verifyMetaSignature(secret string, body []byte, header string) bool {
	if header == "" {
		return false
	}
	expected := header
	expected = strings.TrimPrefix(expected, "sha256=")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	computed := fmt.Sprintf("%x", mac.Sum(nil))
	return hmac.Equal([]byte(computed), []byte(expected))
}

func processInbound(db *database.DB, cfg *config.Config, rawBody []byte) {
	var payload models.WAPayload
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		log.Printf("whatsapp: unmarshal error: %v", err)
		return
	}

	// Guard against status/delivery receipt webhooks (no messages array).
	if len(payload.Entry) == 0 ||
		len(payload.Entry[0].Changes) == 0 ||
		len(payload.Entry[0].Changes[0].Value.Messages) == 0 {
		return
	}

	// Process all messages in the payload (Meta can batch multiple).
	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			for _, msg := range change.Value.Messages {
				handleMessage(db, cfg, &msg)
			}
		}
	}
}

func handleMessage(db *database.DB, cfg *config.Config, msg *models.WAMessage) {
	// Only handle text messages.
	if msg.Type != "text" || msg.Text == nil {
		log.Printf("whatsapp: ignoring non-text message type=%s from=%s", msg.Type, msg.From)
		sendWhatsApp(cfg, msg.From, "Sorry, I can only handle text messages right now.")
		return
	}

	phone := msg.From

	// Per-conversation lock.
	mu := lockFor(phone)
	mu.Lock()
	defer mu.Unlock()

	// Idempotency check.
	exists, err := db.MessageExists(msg.ID)
	if err != nil {
		log.Printf("whatsapp: idempotency check failed: %v", err)
		return
	}
	if exists {
		log.Printf("whatsapp: duplicate message %s, skipping", msg.ID)
		return
	}

	// Upsert conversation.
	if err := db.UpsertConversation(phone); err != nil {
		log.Printf("whatsapp: upsert conversation: %v", err)
		return
	}

	// Check if conversation is PAUSED (staff has taken over).
	status, err := db.GetConversationStatus(phone)
	if err != nil {
		log.Printf("whatsapp: get status: %v", err)
		return
	}
	if status == "PAUSED" {
		log.Printf("whatsapp: conversation %s is PAUSED, sending static reply", phone)
		// Still save the message for audit trail.
		_ = db.InsertMessage(&models.Message{
			ID: msg.ID, ConversationID: phone, Role: "user", Content: msg.Text.Body,
		})
		sendWhatsApp(cfg, phone, "Our team is handling your request directly. We'll be in touch shortly!")
		return
	}

	// Save inbound user message.
	if err := db.InsertMessage(&models.Message{
		ID:             msg.ID,
		ConversationID: phone,
		Role:           "user",
		Content:        msg.Text.Body,
	}); err != nil {
		log.Printf("whatsapp: insert message: %v", err)
		return
	}

	// Load conversation history (last 20 messages).
	history, err := db.GetRecentMessages(phone, 20)
	if err != nil {
		log.Printf("whatsapp: get history: %v", err)
		return
	}

	// Call DeepSeek.
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()

	llmResp, err := llm.Call(ctx, cfg.DeepSeekAPIKey, history)
	if err != nil {
		log.Printf("whatsapp: llm error: %v", err)
		// llmResp is still a valid fallback — continue processing.
	}

	// Save assistant reply.
	assistantMsgID := fmt.Sprintf("assistant-%s-%d", phone, time.Now().UnixNano())
	_ = db.InsertMessage(&models.Message{
		ID:             assistantMsgID,
		ConversationID: phone,
		Role:           "assistant",
		Content:        llmResp.ReplyToUser,
	})

	// Save extracted quote data.
	if dataJSON, err := json.Marshal(llmResp.ExtractedData); err == nil {
		_ = db.UpsertQuoteData(phone, string(dataJSON))
	}

	// Execute action.
	switch llmResp.Action {
	case "handoff":
		if err := sendSlackHandoff(cfg, phone, llmResp); err != nil {
			log.Printf("whatsapp: slack handoff failed: %v — falling back to continue", err)
			// Don't leave customer hanging; send the reply anyway.
		}
		sendWhatsApp(cfg, phone, llmResp.ReplyToUser)

	case "schedule":
		bookingMsg := fmt.Sprintf("%s\n\nYou can pick a time for an on-site assessment here: https://bookings.clearoutspaces.ca/clearoutspaces/assessment", llmResp.ReplyToUser)
		sendWhatsApp(cfg, phone, bookingMsg)

	default: // "continue"
		sendWhatsApp(cfg, phone, llmResp.ReplyToUser)
	}
}

// ─── Outbound WhatsApp ────────────────────────────────────────────────────────

func sendWhatsApp(cfg *config.Config, to, body string) {
	url := fmt.Sprintf("%s/v18.0/%s/messages", metaAPIBaseURL, cfg.MetaPhoneNumberID)
	payload := map[string]any{
		"messaging_product": "whatsapp",
		"to":                to,
		"type":              "text",
		"text":              map[string]string{"body": body},
	}
	payloadBytes, _ := json.Marshal(payload)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payloadBytes))
	if err != nil {
		log.Printf("whatsapp: send: create request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.MetaAccessToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("whatsapp: send: http error: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("whatsapp: send: unexpected status %d: %s", resp.StatusCode, string(body))
	}
}

// ─── Slack handoff ────────────────────────────────────────────────────────────

func sendSlackHandoff(cfg *config.Config, phone string, llmResp *models.LLMResponse) error {
	data := llmResp.ExtractedData
	payload := map[string]any{
		"text": fmt.Sprintf("New Quote Request from +%s", phone),
		"blocks": []any{
			map[string]any{
				"type": "section",
				"text": map[string]any{
					"type": "mrkdwn",
					"text": fmt.Sprintf(
						"*New Quote Request*\n*Phone:* %s\n*Address:* %s\n*Inventory:* %s\n*Stairs:* %s\n*Elevator:* %s",
						phone, data.Address, data.Inventory, data.Stairs, data.ElevatorAccess,
					),
				},
			},
			map[string]any{
				"type": "actions",
				"elements": []any{
					map[string]any{
						"type":      "button",
						"action_id": "take_over_chat",
						"value":     phone,
						"text":      map[string]string{"type": "plain_text", "text": "Take Over Chat"},
					},
				},
			},
		},
	}

	payloadBytes, _ := json.Marshal(payload)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.SlackWebhookURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return fmt.Errorf("slack: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("slack: post error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("slack: unexpected status %d: %s", resp.StatusCode, string(b))
	}
	return nil
}
