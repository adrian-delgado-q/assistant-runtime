package models

import "time"

// ─── WhatsApp inbound payload ────────────────────────────────────────────────

type WAPayload struct {
	Object string    `json:"object"`
	Entry  []WAEntry `json:"entry"`
}

type WAEntry struct {
	Changes []WAChange `json:"changes"`
}

type WAChange struct {
	Value WAValue `json:"value"`
}

type WAValue struct {
	Messages []WAMessage `json:"messages"`
}

type WAMessage struct {
	From string  `json:"from"` // phone number, used as conversation ID
	ID   string  `json:"id"`   // wamid — used for idempotency
	Type string  `json:"type"` // "text", "image", etc.
	Text *WAText `json:"text,omitempty"`
}

type WAText struct {
	Body string `json:"body"`
}

// ─── Database models ─────────────────────────────────────────────────────────

type Conversation struct {
	ID        string    `db:"id"`
	Status    string    `db:"status"` // "ACTIVE" | "PAUSED"
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

type Message struct {
	ID             string    `db:"id"`
	ConversationID string    `db:"conversation_id"`
	Role           string    `db:"role"` // "user" | "assistant" | "system"
	Content        string    `db:"content"`
	CreatedAt      time.Time `db:"created_at"`
}

// ─── LLM contract ────────────────────────────────────────────────────────────

type LLMMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type LLMResponse struct {
	ReplyToUser   string        `json:"reply_to_user"`
	ExtractedData ExtractedData `json:"extracted_data"`
	Action        string        `json:"action"` // "continue" | "handoff" | "schedule"
}

type ExtractedData struct {
	Address        string `json:"address"`
	ElevatorAccess string `json:"elevator_access"`
	Stairs         string `json:"stairs"`
	Inventory      string `json:"inventory"`
}

// ─── Slack interactive payload ────────────────────────────────────────────────

type SlackInteractivePayload struct {
	Type    string        `json:"type"`
	User    SlackUser     `json:"user"`
	Actions []SlackAction `json:"actions"`
}

type SlackUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

type SlackAction struct {
	ActionID string `json:"action_id"`
	Value    string `json:"value"`
}
