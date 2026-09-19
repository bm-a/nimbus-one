// Package session persists agent sessions in SQLite (pure Go via
// modernc.org/sqlite — no CGO, Termux-safe). One database per data dir.
//
// Sessions hold the message history (user/assistant/tool turns with tool
// call IDs preserved for provider replay) plus cumulative token usage.
// Markdown identity/memory files stay in internal/state; this store owns
// the durable, queryable turn record. Writes use a busy timeout and
// synchronous=NORMAL; durability across phone kills comes from
// record-before-execute at the caller, not from fsync-per-row.
package session

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// SchemaVersion is the current user_version pragma.
const SchemaVersion = 1

// Roles.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
	RoleSystem    = "system"
)

// Message is one persisted turn part.
type Message struct {
	ID         int64
	SessionID  string
	Role       string
	Content    string
	Name       string // tool name for RoleTool rows
	ToolCallID string // provider tool-call ID for pairing
	CreatedAt  time.Time
}

// SessionMeta describes a session row.
type SessionMeta struct {
	ID        string
	Name      string
	Workdir   string
	Model     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Usage accumulates token counts and cost per session.
type Usage struct {
	InputTokens  int64
	OutputTokens int64
	CostCents    int64 // millicents to avoid floats: 1 = $0.00001
}

// Store is an open session database.
type Store struct {
	db *sql.DB
}

// Open creates (or opens) path and applies the schema.
func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("session: empty database path")
	}
	db, err := sql.Open("sqlite", path+"?cache=shared")
	if err != nil {
		return nil, fmt.Errorf("session: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		return fmt.Errorf("session: pragma: %w", err)
	}
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return fmt.Errorf("session: user_version: %w", err)
	}
	if v > SchemaVersion {
		return fmt.Errorf("session: database schema v%d newer than supported v%d", v, SchemaVersion)
	}
	if v == SchemaVersion {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS sessions(
			id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '',
			workdir TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS messages(
			id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL,
			role TEXT NOT NULL, content TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '', tool_call_id TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE CASCADE)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, id)`,
		`CREATE TABLE IF NOT EXISTS usage(
			session_id TEXT PRIMARY KEY,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			cost_cents INTEGER NOT NULL DEFAULT 0)`,
		fmt.Sprintf(`PRAGMA user_version = %d`, SchemaVersion),
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("session: migrate: %w", err)
		}
	}
	return tx.Commit()
}

// CreateSession inserts a session row. Empty id generates a time-based one.
func (s *Store) CreateSession(id, name, workdir, model string) (string, error) {
	now := time.Now().UnixMilli()
	if strings.TrimSpace(id) == "" {
		id = fmt.Sprintf("s-%d", now)
	}
	_, err := s.db.Exec(`INSERT INTO sessions(id,name,workdir,model,created_at,updated_at)
		VALUES(?,?,?,?,?,?)`, id, name, workdir, model, now, now)
	if err != nil {
		return "", fmt.Errorf("session: create %s: %w", id, err)
	}
	return id, nil
}

// AppendMessage records one turn part. The session's updated_at advances.
func (s *Store) AppendMessage(sessionID, role, content, name, toolCallID string) (int64, error) {
	switch role {
	case RoleUser, RoleAssistant, RoleTool, RoleSystem:
	default:
		return 0, fmt.Errorf("session: unknown role %q", role)
	}
	now := time.Now().UnixMilli()
	res, err := s.db.Exec(`INSERT INTO messages(session_id,role,content,name,tool_call_id,created_at)
		VALUES(?,?,?,?,?,?)`, sessionID, role, content, name, toolCallID, now)
	if err != nil {
		return 0, fmt.Errorf("session: append: %w", err)
	}
	_, _ = s.db.Exec(`UPDATE sessions SET updated_at=? WHERE id=?`, now, sessionID)
	id, _ := res.LastInsertId()
	return id, nil
}

// History returns all messages for a session in insertion order.
func (s *Store) History(sessionID string) ([]Message, error) {
	rows, err := s.db.Query(`SELECT id,session_id,role,content,name,tool_call_id,created_at
		FROM messages WHERE session_id=? ORDER BY id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("session: history: %w", err)
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var ts int64
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &m.Name, &m.ToolCallID, &ts); err != nil {
			return nil, fmt.Errorf("session: scan: %w", err)
		}
		m.CreatedAt = time.UnixMilli(ts)
		out = append(out, m)
	}
	return out, rows.Err()
}

// RecordUsage adds token counts and cost to a session.
func (s *Store) RecordUsage(sessionID string, in, out, costCents int64) error {
	_, err := s.db.Exec(`INSERT INTO usage(session_id,input_tokens,output_tokens,cost_cents)
		VALUES(?,?,?,?)
		ON CONFLICT(session_id) DO UPDATE SET
			input_tokens=input_tokens+excluded.input_tokens,
			output_tokens=output_tokens+excluded.output_tokens,
			cost_cents=cost_cents+excluded.cost_cents`,
		sessionID, in, out, costCents)
	if err != nil {
		return fmt.Errorf("session: usage: %w", err)
	}
	return nil
}

// GetUsage returns cumulative usage (zero when none recorded).
func (s *Store) GetUsage(sessionID string) (Usage, error) {
	var u Usage
	err := s.db.QueryRow(`SELECT input_tokens,output_tokens,cost_cents FROM usage WHERE session_id=?`,
		sessionID).Scan(&u.InputTokens, &u.OutputTokens, &u.CostCents)
	if err == sql.ErrNoRows {
		return Usage{}, nil
	}
	if err != nil {
		return Usage{}, fmt.Errorf("session: get usage: %w", err)
	}
	return u, nil
}

// ListSessions returns sessions newest-first.
func (s *Store) ListSessions(limit int) ([]SessionMeta, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT id,name,workdir,model,created_at,updated_at
		FROM sessions ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("session: list: %w", err)
	}
	defer rows.Close()
	var out []SessionMeta
	for rows.Next() {
		var m SessionMeta
		var c, u int64
		if err := rows.Scan(&m.ID, &m.Name, &m.Workdir, &m.Model, &c, &u); err != nil {
			return nil, fmt.Errorf("session: scan: %w", err)
		}
		m.CreatedAt = time.UnixMilli(c)
		m.UpdatedAt = time.UnixMilli(u)
		out = append(out, m)
	}
	return out, rows.Err()
}
