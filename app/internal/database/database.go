package database

import (
	"database/sql"
	"log"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"clearoutspaces/internal/models"
)

type DB struct {
	conn *sql.DB
}

// Init opens the SQLite database, applies WAL mode, and runs migrations.
func Init(path string) *DB {
	conn, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		log.Fatalf("database: failed to open: %v", err)
	}
	if err := conn.Ping(); err != nil {
		log.Fatalf("database: failed to ping: %v", err)
	}

	// Limit concurrent writers to avoid SQLITE_BUSY beyond the busy_timeout.
	conn.SetMaxOpenConns(1)

	db := &DB{conn: conn}
	db.migrate()
	log.Println("database: ready")
	return db
}

func (db *DB) migrate() {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS conversations (
id         TEXT PRIMARY KEY,
status     TEXT NOT NULL DEFAULT 'ACTIVE',
created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
)`,
		`CREATE TABLE IF NOT EXISTS messages (
id              TEXT PRIMARY KEY,
conversation_id TEXT NOT NULL,
role            TEXT NOT NULL,
content         TEXT NOT NULL,
created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
FOREIGN KEY(conversation_id) REFERENCES conversations(id)
)`,
		`CREATE TABLE IF NOT EXISTS quote_data (
conversation_id TEXT PRIMARY KEY,
json_dump       TEXT,
updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
FOREIGN KEY(conversation_id) REFERENCES conversations(id)
)`,
	}

	for _, stmt := range migrations {
		if _, err := db.conn.Exec(stmt); err != nil {
			log.Fatalf("database: migration failed: %v", err)
		}
	}
}

// ─── Conversation ─────────────────────────────────────────────────────────────

// UpsertConversation creates a conversation row if it doesn't exist.
func (db *DB) UpsertConversation(phoneNumber string) error {
	_, err := db.conn.Exec(
		`INSERT INTO conversations(id) VALUES(?) ON CONFLICT(id) DO NOTHING`,
		phoneNumber,
	)
	return err
}

// GetConversationStatus returns "ACTIVE" or "PAUSED".
func (db *DB) GetConversationStatus(phoneNumber string) (string, error) {
	var status string
	err := db.conn.QueryRow(
		`SELECT status FROM conversations WHERE id = ?`, phoneNumber,
	).Scan(&status)
	return status, err
}

// PauseConversation sets a conversation's status to PAUSED.
func (db *DB) PauseConversation(phoneNumber string) error {
	_, err := db.conn.Exec(
		`UPDATE conversations SET status = 'PAUSED', updated_at = ? WHERE id = ?`,
		time.Now(), phoneNumber,
	)
	return err
}

// ─── Messages ─────────────────────────────────────────────────────────────────

// MessageExists checks if a wamid has already been processed (idempotency).
func (db *DB) MessageExists(id string) (bool, error) {
	var count int
	err := db.conn.QueryRow(`SELECT COUNT(1) FROM messages WHERE id = ?`, id).Scan(&count)
	return count > 0, err
}

// InsertMessage saves a single message row.
func (db *DB) InsertMessage(m *models.Message) error {
	_, err := db.conn.Exec(
		`INSERT INTO messages(id, conversation_id, role, content) VALUES(?, ?, ?, ?)`,
		m.ID, m.ConversationID, m.Role, m.Content,
	)
	return err
}

// GetRecentMessages returns the last n messages for a conversation, oldest first.
func (db *DB) GetRecentMessages(conversationID string, limit int) ([]models.Message, error) {
	rows, err := db.conn.Query(
		`SELECT id, conversation_id, role, content, created_at
		 FROM messages
		 WHERE conversation_id = ?
		 ORDER BY created_at DESC, rowid DESC
		 LIMIT ?`,
		conversationID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []models.Message
	for rows.Next() {
		var m models.Message
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}

	// Reverse to get chronological order.
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, rows.Err()
}

// ─── Quote Data ───────────────────────────────────────────────────────────────

// UpsertQuoteData saves extracted JSON data for a conversation.
func (db *DB) UpsertQuoteData(conversationID, jsonDump string) error {
	_, err := db.conn.Exec(
		`INSERT INTO quote_data(conversation_id, json_dump, updated_at)
		 VALUES(?, ?, ?)
		 ON CONFLICT(conversation_id) DO UPDATE SET json_dump = excluded.json_dump, updated_at = excluded.updated_at`,
		conversationID, jsonDump, time.Now(),
	)
	return err
}
