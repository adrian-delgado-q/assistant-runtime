package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"clearoutspaces/internal/config"
	"clearoutspaces/internal/database"
	"clearoutspaces/internal/models"
)

// HandleSlackInteractive processes the "Take Over Chat" button click from Slack.
func HandleSlackInteractive(db *database.DB, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1. Read raw body first — required for signature verification.
		rawBody, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// 2. Verify Slack signature.
		timestamp := r.Header.Get("X-Slack-Request-Timestamp")
		signature := r.Header.Get("X-Slack-Signature")

		if !verifySlackSignature(cfg.SlackSigningSecret, timestamp, rawBody, signature) {
			log.Println("slack: invalid signature")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		// 3. Decode form-encoded body and extract the JSON payload parameter.
		formVals, err := url.ParseQuery(string(rawBody))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		payloadJSON := formVals.Get("payload")
		if payloadJSON == "" {
			http.Error(w, "missing payload", http.StatusBadRequest)
			return
		}

		var slackPayload models.SlackInteractivePayload
		if err := json.Unmarshal([]byte(payloadJSON), &slackPayload); err != nil {
			log.Printf("slack: unmarshal payload: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if len(slackPayload.Actions) == 0 {
			http.Error(w, "no actions", http.StatusBadRequest)
			return
		}

		action := slackPayload.Actions[0]
		if action.ActionID != "take_over_chat" {
			w.WriteHeader(http.StatusOK)
			return
		}

		phone := action.Value

		// 4. Validate phone exists in DB before acting (prevents arbitrary pausing).
		status, err := db.GetConversationStatus(phone)
		if err != nil {
			log.Printf("slack: conversation %s not found: %v", phone, err)
			w.Header().Set("Content-Type", "application/json")
			writeJSON(w, map[string]any{"replace_original": true, "text": "⚠️ Conversation not found."})
			return
		}

		if status == "PAUSED" {
			w.Header().Set("Content-Type", "application/json")
			writeJSON(w, map[string]any{"replace_original": true, "text": "ℹ️ Chat was already paused."})
			return
		}

		// 5. Pause the conversation.
		if err := db.PauseConversation(phone); err != nil {
			log.Printf("slack: pause conversation %s: %v", phone, err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		log.Printf("slack: conversation %s paused by %s", phone, slackPayload.User.Username)

		// 6. Respond to Slack within 3 seconds.
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, map[string]any{
			"replace_original": true,
			"text":             fmt.Sprintf("✅ Chat paused. %s has taken over the conversation.", slackPayload.User.Username),
		})
	}
}

// writeJSON encodes v as JSON to w, logging any error.
func writeJSON(w http.ResponseWriter, v any) {
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("slack: encode response: %v", err)
	}
}

// verifySlackSignature validates the Slack request signature.
// See: https://api.slack.com/authentication/verifying-requests-from-slack
func verifySlackSignature(signingSecret, timestamp string, body []byte, signature string) bool {
	if timestamp == "" || signature == "" {
		return false
	}

	// Reject requests older than 5 minutes (replay attack prevention).
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	if time.Now().Unix()-ts > 300 {
		log.Println("slack: request timestamp too old")
		return false
	}

	baseString := fmt.Sprintf("v0:%s:%s", timestamp, string(body))
	mac := hmac.New(sha256.New, []byte(signingSecret))
	mac.Write([]byte(baseString))
	computed := fmt.Sprintf("v0=%x", mac.Sum(nil))

	return hmac.Equal([]byte(computed), []byte(signature))
}
