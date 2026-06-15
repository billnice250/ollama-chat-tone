package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct{ DB *sql.DB }

const (
	sqliteBusyTimeout = 5 * time.Second
	sqliteRetryBase   = 50 * time.Millisecond
	sqliteRetryMax    = 5
	sqliteTimeLayout  = "2006-01-02 15:04:05"
)

type Conversation struct {
	ID           string    `json:"id"`
	UserEmail    string    `json:"-"`
	Title        string    `json:"title"`
	CreatedAt    string    `json:"createdAt"`
	UpdatedAt    string    `json:"updatedAt"`
	MessageCount int       `json:"messageCount"`
	Messages     []Message `json:"messages,omitempty"`
}

type Message struct {
	ID             int64  `json:"id"`
	ConversationID string `json:"conversationId"`
	Role           string `json:"role"`
	Content        string `json:"content"`
	Thinking       string `json:"thinking,omitempty"`
	Model          string `json:"model,omitempty"`
	CreatedAt      string `json:"createdAt"`
}

type ChatJob struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversationId"`
	UserEmail      string `json:"-"`
	Status         string `json:"status"`
	Content        string `json:"content"`
	Thinking       string `json:"thinking,omitempty"`
	Model          string `json:"model,omitempty"`
	Error          string `json:"error,omitempty"`
	MessageID      int64  `json:"messageId,omitempty"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}

type User struct {
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
	Approved     bool   `json:"approved"`
	IsAdmin      bool   `json:"isAdmin"`
	Active       bool   `json:"active"`
	LastSeenAt   string `json:"lastSeenAt"`
	CreatedAt    string `json:"createdAt"`
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	configureSQLite(db)
	if err := applySQLitePragmas(db); err != nil {
		return nil, err
	}
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS conversations (
  id TEXT PRIMARY KEY,
  user_email TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT 'New chat',
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS messages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  conversation_id TEXT NOT NULL,
  role TEXT NOT NULL,
  content TEXT NOT NULL,
  thinking TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS users (
  username TEXT PRIMARY KEY,
  password_hash TEXT NOT NULL,
  approved INTEGER NOT NULL DEFAULT 0,
  is_admin INTEGER NOT NULL DEFAULT 0,
  last_seen_at DATETIME DEFAULT NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS chat_jobs (
  id TEXT PRIMARY KEY,
  conversation_id TEXT NOT NULL,
  user_email TEXT NOT NULL,
  status TEXT NOT NULL,
  content TEXT NOT NULL DEFAULT '',
  thinking TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  message_id INTEGER NOT NULL DEFAULT 0,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);`)
	if err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		return nil, err
	}
	return &Store{DB: db}, nil
}

func configureSQLite(db *sql.DB) {
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
}

func applySQLitePragmas(db *sql.DB) error {
	stmts := []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA synchronous = NORMAL`,
		`PRAGMA foreign_keys = ON`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func migrate(db *sql.DB) error {
	stmts := []string{
		`ALTER TABLE conversations ADD COLUMN updated_at DATETIME DEFAULT CURRENT_TIMESTAMP`,
		`ALTER TABLE messages ADD COLUMN thinking TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE messages ADD COLUMN model TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN approved INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE users ADD COLUMN is_admin INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE users ADD COLUMN last_seen_at DATETIME DEFAULT NULL`,
		`ALTER TABLE chat_jobs ADD COLUMN message_id INTEGER NOT NULL DEFAULT 0`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil && !isDuplicateColumn(err) {
			return err
		}
	}
	return nil
}

func (s *Store) EnsureAdmin(ctx context.Context, username, passwordHash string) error {
	if username == "" || passwordHash == "" {
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO users (username, password_hash, approved, is_admin, created_at)
VALUES (?, ?, 1, 1, CURRENT_TIMESTAMP)
ON CONFLICT(username) DO UPDATE SET password_hash = excluded.password_hash, approved = 1, is_admin = 1`, username, passwordHash)
	return err
}

func (s *Store) CreateUser(ctx context.Context, username, passwordHash string) error {
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO users (username, password_hash, approved, is_admin, created_at)
VALUES (?, ?, 0, 0, CURRENT_TIMESTAMP)`, username, passwordHash)
	return err
}

func (s *Store) EnsurePendingUser(ctx context.Context, username string) (*User, error) {
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO users (username, password_hash, approved, is_admin, created_at)
VALUES (?, '', 0, 0, CURRENT_TIMESTAMP)
ON CONFLICT(username) DO NOTHING`, username)
	if err != nil {
		return nil, err
	}
	return s.GetUser(ctx, username)
}

func (s *Store) GetUser(ctx context.Context, username string) (*User, error) {
	var u User
	var approved, isAdmin, active int
	var lastSeen sql.NullString
	err := s.DB.QueryRowContext(ctx, `
SELECT username, password_hash, approved, is_admin, last_seen_at, created_at,
       CASE WHEN last_seen_at IS NOT NULL AND datetime(last_seen_at) >= datetime('now', '-5 minutes') THEN 1 ELSE 0 END
FROM users
WHERE username = ?`, username).Scan(&u.Username, &u.PasswordHash, &approved, &isAdmin, &lastSeen, &u.CreatedAt, &active)
	if err != nil {
		return nil, err
	}
	u.Approved = approved == 1
	u.IsAdmin = isAdmin == 1
	if lastSeen.Valid {
		u.LastSeenAt = lastSeen.String
	}
	u.Active = active == 1
	return &u, nil
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT username, password_hash, approved, is_admin, last_seen_at, created_at,
       CASE WHEN last_seen_at IS NOT NULL AND datetime(last_seen_at) >= datetime('now', '-5 minutes') THEN 1 ELSE 0 END AS active
FROM users
ORDER BY is_admin DESC, active DESC, approved ASC, COALESCE(last_seen_at, created_at) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		var u User
		var approved, isAdmin, active int
		var lastSeen sql.NullString
		if err := rows.Scan(&u.Username, &u.PasswordHash, &approved, &isAdmin, &lastSeen, &u.CreatedAt, &active); err != nil {
			return nil, err
		}
		u.Approved = approved == 1
		u.IsAdmin = isAdmin == 1
		u.Active = active == 1
		if lastSeen.Valid {
			u.LastSeenAt = lastSeen.String
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) TouchUser(ctx context.Context, username string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET last_seen_at = CURRENT_TIMESTAMP WHERE username = ?`, username)
	return err
}

func (s *Store) ApproveUser(ctx context.Context, username string) error {
	return s.SetUserApproved(ctx, username, true)
}

func (s *Store) RevokeUser(ctx context.Context, username string) error {
	return s.SetUserApproved(ctx, username, false)
}

func (s *Store) SetUserApproved(ctx context.Context, username string, approved bool) error {
	value := 0
	if approved {
		value = 1
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE users SET approved = ? WHERE username = ? AND is_admin = 0`, value, username)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func isDuplicateColumn(err error) bool {
	return err != nil && (errors.Is(err, sql.ErrNoRows) || strings.Contains(err.Error(), "duplicate column name"))
}

func (s *Store) ListConversations(ctx context.Context, user string) ([]Conversation, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT c.id, c.user_email, c.title, c.created_at, c.updated_at, COUNT(m.id)
FROM conversations c
LEFT JOIN messages m ON m.conversation_id = c.id
WHERE c.user_email = ?
GROUP BY c.id, c.user_email, c.title, c.created_at, c.updated_at
ORDER BY c.updated_at DESC, c.created_at DESC`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.UserEmail, &c.Title, &c.CreatedAt, &c.UpdatedAt, &c.MessageCount); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CreateConversation(ctx context.Context, user, title string) (*Conversation, error) {
	if title == "" {
		title = "New chat"
	}
	c := &Conversation{ID: newID(), UserEmail: user, Title: title}
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO conversations (id, user_email, title, created_at, updated_at)
VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, c.ID, c.UserEmail, c.Title)
	if err != nil {
		return nil, err
	}
	return s.GetConversation(ctx, user, c.ID)
}

func (s *Store) GetConversation(ctx context.Context, user, id string) (*Conversation, error) {
	var c Conversation
	err := s.DB.QueryRowContext(ctx, `
SELECT id, user_email, title, created_at, updated_at
FROM conversations
WHERE user_email = ? AND id = ?`, user, id).Scan(&c.ID, &c.UserEmail, &c.Title, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	messages, err := s.ListMessages(ctx, user, id)
	if err != nil {
		return nil, err
	}
	c.Messages = messages
	c.MessageCount = len(messages)
	return &c, nil
}

func (s *Store) ListMessages(ctx context.Context, user, conversationID string) ([]Message, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT m.id, m.conversation_id, m.role, m.content, m.thinking, m.model, m.created_at
FROM messages m
JOIN conversations c ON c.id = m.conversation_id
WHERE c.user_email = ? AND m.conversation_id = ?
ORDER BY m.id ASC`, user, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.Thinking, &m.Model, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) AddMessage(ctx context.Context, user, conversationID string, msg Message) (*Message, error) {
	var exists int
	if err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM conversations WHERE user_email = ? AND id = ?`, user, conversationID).Scan(&exists); err != nil {
		return nil, err
	}
	var res sql.Result
	err := retryBusyWrite(ctx, func() error {
		var execErr error
		res, execErr = s.DB.ExecContext(ctx, `
INSERT INTO messages (conversation_id, role, content, thinking, model, created_at)
VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`, conversationID, msg.Role, msg.Content, msg.Thinking, msg.Model)
		return execErr
	})
	if err != nil {
		return nil, err
	}
	if err := s.touchConversation(ctx, conversationID); err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetMessage(ctx, user, conversationID, id)
}

func (s *Store) UpdateConversationTitle(ctx context.Context, user, id, title string) error {
	if title == "" {
		title = "New chat"
	}
	res, err := s.DB.ExecContext(ctx, `
UPDATE conversations SET title = ?, updated_at = CURRENT_TIMESTAMP
WHERE user_email = ? AND id = ?`, title, user, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteConversation(ctx context.Context, user, id string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `DELETE FROM conversations WHERE user_email = ? AND id = ?`, user, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE conversation_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM chat_jobs WHERE conversation_id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteAccount(ctx context.Context, user string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `SELECT id FROM conversations WHERE user_email = ?`, user)
	if err != nil {
		return err
	}
	var conversationIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		conversationIDs = append(conversationIDs, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, id := range conversationIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE conversation_id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM chat_jobs WHERE conversation_id = ?`, id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM conversations WHERE user_email = ?`, user); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE username = ?`, user); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) GetMessage(ctx context.Context, user, conversationID string, id int64) (*Message, error) {
	var m Message
	err := s.DB.QueryRowContext(ctx, `
SELECT m.id, m.conversation_id, m.role, m.content, m.thinking, m.model, m.created_at
FROM messages m
JOIN conversations c ON c.id = m.conversation_id
WHERE c.user_email = ? AND m.conversation_id = ? AND m.id = ?`, user, conversationID, id).
		Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.Thinking, &m.Model, &m.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Store) CreateChatJob(ctx context.Context, user, conversationID, model string) (*ChatJob, error) {
	var exists int
	if err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM conversations WHERE user_email = ? AND id = ?`, user, conversationID).Scan(&exists); err != nil {
		return nil, err
	}
	id := newID()
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO chat_jobs (id, conversation_id, user_email, status, model, created_at, updated_at)
VALUES (?, ?, ?, 'running', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, id, conversationID, user, model)
	if err != nil {
		return nil, err
	}
	return s.GetChatJob(ctx, user, conversationID, id, 0, "")
}

func (s *Store) GetChatJob(ctx context.Context, user, conversationID, id string, staleAfter time.Duration, staleMessage string) (*ChatJob, error) {
	if err := s.failStaleChatJob(ctx, user, conversationID, id, staleAfter, staleMessage); err != nil {
		return nil, err
	}
	var j ChatJob
	err := s.DB.QueryRowContext(ctx, `
SELECT id, conversation_id, user_email, status, content, thinking, model, error, message_id, created_at, updated_at
FROM chat_jobs
WHERE user_email = ? AND conversation_id = ? AND id = ?`, user, conversationID, id).
		Scan(&j.ID, &j.ConversationID, &j.UserEmail, &j.Status, &j.Content, &j.Thinking, &j.Model, &j.Error, &j.MessageID, &j.CreatedAt, &j.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &j, nil
}

func (s *Store) ActiveChatJob(ctx context.Context, user, conversationID string, staleAfter time.Duration, staleMessage string) (*ChatJob, error) {
	if err := s.failStaleConversationChatJobs(ctx, user, conversationID, staleAfter, staleMessage); err != nil {
		return nil, err
	}
	var j ChatJob
	err := s.DB.QueryRowContext(ctx, `
SELECT id, conversation_id, user_email, status, content, thinking, model, error, message_id, created_at, updated_at
FROM chat_jobs
WHERE user_email = ? AND conversation_id = ? AND status = 'running'
ORDER BY updated_at DESC, created_at DESC
LIMIT 1`, user, conversationID).
		Scan(&j.ID, &j.ConversationID, &j.UserEmail, &j.Status, &j.Content, &j.Thinking, &j.Model, &j.Error, &j.MessageID, &j.CreatedAt, &j.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &j, nil
}

func (s *Store) UpdateChatJob(ctx context.Context, user, conversationID, id, content, thinking string) error {
	return retryBusyWrite(ctx, func() error {
		_, err := s.DB.ExecContext(ctx, `
UPDATE chat_jobs
SET content = ?, thinking = ?, updated_at = CURRENT_TIMESTAMP
WHERE user_email = ? AND conversation_id = ? AND id = ? AND status = 'running'`, content, thinking, user, conversationID, id)
		return err
	})
}

func (s *Store) CompleteChatJob(ctx context.Context, user, conversationID, id, content, thinking, model string) (*Message, error) {
	return retryBusyResult(ctx, func() (*Message, error) {
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()

		res, err := tx.ExecContext(ctx, `
INSERT INTO messages (conversation_id, role, content, thinking, model, created_at)
VALUES (?, 'assistant', ?, ?, ?, CURRENT_TIMESTAMP)`, conversationID, content, thinking, model)
		if err != nil {
			return nil, err
		}
		messageID, err := res.LastInsertId()
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE chat_jobs
SET status = 'complete', content = ?, thinking = ?, model = ?, message_id = ?, updated_at = CURRENT_TIMESTAMP
WHERE user_email = ? AND conversation_id = ? AND id = ?`, content, thinking, model, messageID, user, conversationID, id); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE conversations
SET updated_at = CURRENT_TIMESTAMP
WHERE user_email = ? AND id = ?`, user, conversationID); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &Message{
			ID:             messageID,
			ConversationID: conversationID,
			Role:           "assistant",
			Content:        content,
			Thinking:       thinking,
			Model:          model,
			CreatedAt:      time.Now().UTC().Format(sqliteTimeLayout),
		}, nil
	})
}

func (s *Store) FailChatJob(ctx context.Context, user, conversationID, id, message string) error {
	return retryBusyWrite(ctx, func() error {
		_, err := s.DB.ExecContext(ctx, `
UPDATE chat_jobs
SET status = 'error', error = ?, updated_at = CURRENT_TIMESTAMP
WHERE user_email = ? AND conversation_id = ? AND id = ? AND status = 'running'`, message, user, conversationID, id)
		return err
	})
}

func (s *Store) CancelChatJob(ctx context.Context, user, conversationID, id string) error {
	var res sql.Result
	err := retryBusyWrite(ctx, func() error {
		var execErr error
		res, execErr = s.DB.ExecContext(ctx, `
UPDATE chat_jobs
SET status = 'canceled', updated_at = CURRENT_TIMESTAMP
WHERE user_email = ? AND conversation_id = ? AND id = ? AND status = 'running'`, user, conversationID, id)
		return execErr
	})
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) FailStaleChatJobs(ctx context.Context, staleAfter time.Duration, message string) error {
	if staleAfter <= 0 {
		return nil
	}
	cutoff := staleCutoff(staleAfter)
	return retryBusyWrite(ctx, func() error {
		_, err := s.DB.ExecContext(ctx, `
UPDATE chat_jobs
SET status = 'error', error = ?, updated_at = CURRENT_TIMESTAMP
WHERE status = 'running' AND updated_at <= ?`, message, cutoff)
		return err
	})
}

func (s *Store) failStaleConversationChatJobs(ctx context.Context, user, conversationID string, staleAfter time.Duration, message string) error {
	if staleAfter <= 0 {
		return nil
	}
	cutoff := staleCutoff(staleAfter)
	return retryBusyWrite(ctx, func() error {
		_, err := s.DB.ExecContext(ctx, `
UPDATE chat_jobs
SET status = 'error', error = ?, updated_at = CURRENT_TIMESTAMP
WHERE user_email = ? AND conversation_id = ? AND status = 'running' AND updated_at <= ?`, message, user, conversationID, cutoff)
		return err
	})
}

func (s *Store) failStaleChatJob(ctx context.Context, user, conversationID, id string, staleAfter time.Duration, message string) error {
	if staleAfter <= 0 {
		return nil
	}
	cutoff := staleCutoff(staleAfter)
	return retryBusyWrite(ctx, func() error {
		_, err := s.DB.ExecContext(ctx, `
UPDATE chat_jobs
SET status = 'error', error = ?, updated_at = CURRENT_TIMESTAMP
WHERE user_email = ? AND conversation_id = ? AND id = ? AND status = 'running' AND updated_at <= ?`, message, user, conversationID, id, cutoff)
		return err
	})
}

func (s *Store) touchConversation(ctx context.Context, conversationID string) error {
	return retryBusyWrite(ctx, func() error {
		_, err := s.DB.ExecContext(ctx, `
UPDATE conversations
SET updated_at = CURRENT_TIMESTAMP
WHERE id = ?`, conversationID)
		return err
	})
}

func staleCutoff(staleAfter time.Duration) string {
	return time.Now().Add(-staleAfter).UTC().Format(sqliteTimeLayout)
}

func retryBusyWrite(ctx context.Context, fn func() error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var err error
	delay := sqliteRetryBase
	for attempt := 0; attempt < sqliteRetryMax; attempt++ {
		err = fn()
		if !isBusyError(err) {
			return err
		}
		if attempt == sqliteRetryMax-1 {
			return err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		delay *= 2
	}
	return err
}

func retryBusyResult[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var zero T
	var result T
	err := retryBusyWrite(ctx, func() error {
		var innerErr error
		result, innerErr = fn()
		if innerErr != nil {
			result = zero
		}
		return innerErr
	})
	if err != nil {
		return zero, err
	}
	return result, nil
}

func isBusyError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") || strings.Contains(message, "sqlite_busy")
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b[:])
}
