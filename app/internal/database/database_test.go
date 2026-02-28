package database

import (
	"fmt"
	"testing"

	"clearoutspaces/internal/models"
)

// newTestDB creates an in-memory SQLite database for testing.
func newTestDB(t *testing.T) *DB {
	t.Helper()
	db := Init(":memory:")
	t.Cleanup(func() { db.conn.Close() })
	return db
}

// ─── Conversation tests ───────────────────────────────────────────────────────

func TestUpsertConversation_CreatesNew(t *testing.T) {
	db := newTestDB(t)

	if err := db.UpsertConversation("14165551234"); err != nil {
		t.Fatalf("UpsertConversation: unexpected error: %v", err)
	}

	status, err := db.GetConversationStatus("14165551234")
	if err != nil {
		t.Fatalf("GetConversationStatus: unexpected error: %v", err)
	}
	if status != "ACTIVE" {
		t.Errorf("expected status ACTIVE, got %s", status)
	}
}

func TestUpsertConversation_Idempotent(t *testing.T) {
	db := newTestDB(t)

	// Insert twice — should not error or change status.
	if err := db.UpsertConversation("14165551234"); err != nil {
		t.Fatal(err)
	}
	if err := db.PauseConversation("14165551234"); err != nil {
		t.Fatal(err)
	}
	// Upsert again must not reset the status back to ACTIVE.
	if err := db.UpsertConversation("14165551234"); err != nil {
		t.Fatal(err)
	}

	status, err := db.GetConversationStatus("14165551234")
	if err != nil {
		t.Fatal(err)
	}
	if status != "PAUSED" {
		t.Errorf("expected PAUSED after idempotent upsert, got %s", status)
	}
}

func TestGetConversationStatus_NotFound(t *testing.T) {
	db := newTestDB(t)

	_, err := db.GetConversationStatus("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent conversation, got nil")
	}
}

func TestPauseConversation(t *testing.T) {
	db := newTestDB(t)

	if err := db.UpsertConversation("14165551234"); err != nil {
		t.Fatal(err)
	}
	if err := db.PauseConversation("14165551234"); err != nil {
		t.Fatalf("PauseConversation: unexpected error: %v", err)
	}

	status, err := db.GetConversationStatus("14165551234")
	if err != nil {
		t.Fatal(err)
	}
	if status != "PAUSED" {
		t.Errorf("expected PAUSED, got %s", status)
	}
}

// ─── Message tests ───────────────────────────────────────────────────────────

func TestInsertMessage_AndExists(t *testing.T) {
	db := newTestDB(t)
	if err := db.UpsertConversation("14165551234"); err != nil {
		t.Fatal(err)
	}

	msg := &models.Message{
		ID:             "wamid.test123",
		ConversationID: "14165551234",
		Role:           "user",
		Content:        "I need a couch removed.",
	}
	if err := db.InsertMessage(msg); err != nil {
		t.Fatalf("InsertMessage: unexpected error: %v", err)
	}

	exists, err := db.MessageExists("wamid.test123")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("expected message to exist, got false")
	}
}

func TestMessageExists_False(t *testing.T) {
	db := newTestDB(t)

	exists, err := db.MessageExists("nonexistent-id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists {
		t.Error("expected false for nonexistent message, got true")
	}
}

func TestInsertMessage_DuplicateID_Errors(t *testing.T) {
	db := newTestDB(t)
	if err := db.UpsertConversation("14165551234"); err != nil {
		t.Fatal(err)
	}

	msg := &models.Message{
		ID:             "wamid.dup",
		ConversationID: "14165551234",
		Role:           "user",
		Content:        "hello",
	}
	if err := db.InsertMessage(msg); err != nil {
		t.Fatal(err)
	}
	// Second insert with same ID must fail (PRIMARY KEY constraint).
	if err := db.InsertMessage(msg); err == nil {
		t.Error("expected error on duplicate message ID, got nil")
	}
}

func TestGetRecentMessages_Order(t *testing.T) {
	db := newTestDB(t)
	phone := "14165551234"
	if err := db.UpsertConversation(phone); err != nil {
		t.Fatal(err)
	}

	contents := []string{"first", "second", "third"}
	for i, c := range contents {
		err := db.InsertMessage(&models.Message{
			ID:             fmt.Sprintf("msg-%d", i),
			ConversationID: phone,
			Role:           "user",
			Content:        c,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	msgs, err := db.GetRecentMessages(phone, 10)
	if err != nil {
		t.Fatalf("GetRecentMessages: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	for i, want := range contents {
		if msgs[i].Content != want {
			t.Errorf("message[%d]: expected %q, got %q", i, want, msgs[i].Content)
		}
	}
}

func TestGetRecentMessages_Limit(t *testing.T) {
	db := newTestDB(t)
	phone := "14165551234"
	if err := db.UpsertConversation(phone); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		_ = db.InsertMessage(&models.Message{
			ID:             fmt.Sprintf("msg-%d", i),
			ConversationID: phone,
			Role:           "user",
			Content:        fmt.Sprintf("msg %d", i),
		})
	}

	msgs, err := db.GetRecentMessages(phone, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Errorf("expected 3 (limit), got %d", len(msgs))
	}
}

func TestGetRecentMessages_Empty(t *testing.T) {
	db := newTestDB(t)
	if err := db.UpsertConversation("14165551234"); err != nil {
		t.Fatal(err)
	}

	msgs, err := db.GetRecentMessages("14165551234", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
}

// ─── Quote data tests ─────────────────────────────────────────────────────────

func TestUpsertQuoteData(t *testing.T) {
	db := newTestDB(t)
	if err := db.UpsertConversation("14165551234"); err != nil {
		t.Fatal(err)
	}

	json1 := `{"address":"123 Main St","inventory":"1 couch"}`
	if err := db.UpsertQuoteData("14165551234", json1); err != nil {
		t.Fatalf("UpsertQuoteData: %v", err)
	}

	// Update with new data — no error expected.
	json2 := `{"address":"123 Main St","inventory":"1 couch, 2 chairs"}`
	if err := db.UpsertQuoteData("14165551234", json2); err != nil {
		t.Fatalf("UpsertQuoteData (update): %v", err)
	}

	// Verify the latest value is stored.
	var stored string
	err := db.conn.QueryRow(
		`SELECT json_dump FROM quote_data WHERE conversation_id = ?`, "14165551234",
	).Scan(&stored)
	if err != nil {
		t.Fatal(err)
	}
	if stored != json2 {
		t.Errorf("expected %q, got %q", json2, stored)
	}
}
