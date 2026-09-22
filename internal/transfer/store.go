// Package transfer implements Vault's phone transfers: short-lived,
// single-purpose links shown as QR codes: uploads (phone → Vault),
// downloads (Vault → phone) and read-only share links.
//
// Tokens are 256-bit random values. Only their SHA-256 hash is stored, so
// the database never holds a usable link.
package transfer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver

	"github.com/TouchWorkStation/Omarchy-Vault/internal/auth"
)

// Kind is what a session allows.
type Kind string

const (
	KindUpload   Kind = "upload"
	KindDownload Kind = "download"
	KindShare    Kind = "share"
)

// Unlimited is the MaxFiles value of a link with no download limit.
const Unlimited = math.MaxInt32

// NoByteLimit is the MaxBytes value of download and share links.
const NoByteLimit = int64(1) << 62

// Session is a transfer link as stored. For uploads Folder is where files
// go and Files/MaxFiles count files received. For downloads and shares
// Path is the Vault-relative file or folder offered, Folder its top-level
// Vault folder, and Files/MaxFiles count downloads.
type Session struct {
	ID        string    `json:"id"`
	Kind      Kind      `json:"kind"`
	Folder    string    `json:"folder"`
	Path      string    `json:"path,omitempty"`
	CreatedBy string    `json:"created_by"`
	Client    string    `json:"client"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Revoked   bool      `json:"revoked"`
	MaxFiles  int       `json:"max_files"`
	MaxBytes  int64     `json:"max_bytes"`
	Files     int       `json:"files"`
	Bytes     int64     `json:"bytes"`
	// HasPassword is set for share links that need a password.
	HasPassword bool `json:"has_password"`

	passwordHash string
}

// CheckPassword verifies a share link's password (argon2id). Links without
// one accept any value.
func (s Session) CheckPassword(pw string) bool {
	if s.passwordHash == "" {
		return true
	}
	return auth.VerifyPassword(pw, s.passwordHash)
}

// Active reports whether the session can still be used at t.
func (s Session) Active(t time.Time) bool {
	return !s.Revoked && t.Before(s.ExpiresAt) && s.Files < s.MaxFiles && s.Bytes < s.MaxBytes
}

// Errors from lookups. Callers show one friendly message for all of them.
var (
	ErrNotFound = errors.New("transfer: no such link")
	ErrExpired  = errors.New("transfer: link expired")
	ErrRevoked  = errors.New("transfer: link was stopped")
	ErrUsedUp   = errors.New("transfer: link limit reached")
)

// Store keeps sessions and the activity log in SQLite.
type Store struct {
	db  *sql.DB
	Now func() time.Time
}

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	id          TEXT PRIMARY KEY,
	token_hash  BLOB NOT NULL UNIQUE,
	kind        TEXT NOT NULL,
	folder      TEXT NOT NULL,
	created_by  TEXT NOT NULL,
	client      TEXT NOT NULL DEFAULT '',
	created_at  INTEGER NOT NULL,
	expires_at  INTEGER NOT NULL,
	revoked_at  INTEGER,
	max_files   INTEGER NOT NULL,
	max_bytes   INTEGER NOT NULL,
	files       INTEGER NOT NULL DEFAULT 0,
	bytes       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS sessions_expires ON sessions(expires_at);
CREATE TABLE IF NOT EXISTS activity (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	at          INTEGER NOT NULL,
	session_id  TEXT,
	kind        TEXT NOT NULL,
	folder      TEXT NOT NULL,
	name        TEXT NOT NULL,
	size        INTEGER NOT NULL,
	actor       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS activity_at ON activity(at);
CREATE INDEX IF NOT EXISTS activity_session ON activity(session_id);
`

// migrations add columns introduced after a database was created.
var migrations = []struct{ column, def string }{
	{"path", "TEXT NOT NULL DEFAULT ''"},          // Milestone 5
	{"password_hash", "TEXT NOT NULL DEFAULT ''"}, // Milestone 5
}

func migrate(db *sql.DB) error {
	have := map[string]bool{}
	rows, err := db.Query(`SELECT name FROM pragma_table_info('sessions')`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		have[name] = true
	}
	rows.Close()
	for _, m := range migrations {
		if !have[m.column] {
			if _, err := db.Exec(`ALTER TABLE sessions ADD COLUMN ` + m.column + ` ` + m.def); err != nil {
				return err
			}
		}
	}
	return nil
}

// Open opens (creating if needed) the database at path with 0600
// permissions in a 0700 directory.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600); err == nil {
		f.Close()
	}
	_ = os.Chmod(path, 0o600)
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: one writer; keeps things simple and safe
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("transfer: init db: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("transfer: upgrade db: %w", err)
	}
	return &Store{db: db, Now: time.Now}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func tokenHash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// NewSession is the input to Create.
type NewSession struct {
	Kind      Kind
	Folder    string
	CreatedBy string
	Client    string
	TTL       time.Duration
	MaxFiles  int
	MaxBytes  int64
	// Path and PasswordHash are for downloads and shares.
	Path         string
	PasswordHash string
}

// Create stores a session and returns it with its one-time token. The
// token is never stored and cannot be recovered later.
func (s *Store) Create(ctx context.Context, n NewSession) (Session, string, error) {
	id, err := auth.Random(12)
	if err != nil {
		return Session{}, "", err
	}
	token, err := auth.Random(32)
	if err != nil {
		return Session{}, "", err
	}
	now := s.now()
	sess := Session{
		ID: id, Kind: n.Kind, Folder: n.Folder, CreatedBy: n.CreatedBy, Client: n.Client,
		CreatedAt: now, ExpiresAt: now.Add(n.TTL), MaxFiles: n.MaxFiles, MaxBytes: n.MaxBytes,
		Path: n.Path, HasPassword: n.PasswordHash != "", passwordHash: n.PasswordHash,
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO sessions
		(id, token_hash, kind, folder, created_by, client, created_at, expires_at, max_files, max_bytes, path, password_hash)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		sess.ID, tokenHash(token), string(sess.Kind), sess.Folder, sess.CreatedBy, sess.Client,
		sess.CreatedAt.UnixMilli(), sess.ExpiresAt.UnixMilli(), sess.MaxFiles, sess.MaxBytes, sess.Path, n.PasswordHash)
	if err != nil {
		return Session{}, "", err
	}
	return sess, token, nil
}

const cols = `id, kind, folder, created_by, client, created_at, expires_at, revoked_at, max_files, max_bytes, files, bytes, path, password_hash`

func scan(row interface{ Scan(...any) error }) (Session, error) {
	var sess Session
	var kind string
	var created, expires int64
	var revoked sql.NullInt64
	err := row.Scan(&sess.ID, &kind, &sess.Folder, &sess.CreatedBy, &sess.Client, &created, &expires,
		&revoked, &sess.MaxFiles, &sess.MaxBytes, &sess.Files, &sess.Bytes, &sess.Path, &sess.passwordHash)
	if err != nil {
		return Session{}, err
	}
	sess.Kind = Kind(kind)
	sess.CreatedAt = time.UnixMilli(created)
	sess.ExpiresAt = time.UnixMilli(expires)
	sess.Revoked = revoked.Valid
	sess.HasPassword = sess.passwordHash != ""
	return sess, nil
}

// Lookup finds the session for a token and checks it is usable.
func (s *Store) Lookup(ctx context.Context, token string, kind Kind) (Session, error) {
	if len(token) < 40 || len(token) > 64 {
		return Session{}, ErrNotFound
	}
	sess, err := scan(s.db.QueryRowContext(ctx, `SELECT `+cols+` FROM sessions WHERE token_hash = ? AND kind = ?`, tokenHash(token), string(kind)))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	now := s.now()
	switch {
	case sess.Revoked:
		return sess, ErrRevoked
	case !now.Before(sess.ExpiresAt):
		return sess, ErrExpired
	case sess.Files >= sess.MaxFiles || sess.Bytes >= sess.MaxBytes:
		return sess, ErrUsedUp
	}
	return sess, nil
}

// Get returns a session by its public id.
func (s *Store) Get(ctx context.Context, id string) (Session, error) {
	sess, err := scan(s.db.QueryRowContext(ctx, `SELECT `+cols+` FROM sessions WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return sess, err
}

// Active lists usable sessions, newest first.
func (s *Store) Active(ctx context.Context) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+cols+` FROM sessions
		WHERE revoked_at IS NULL AND expires_at > ? AND files < max_files AND bytes < max_bytes
		ORDER BY created_at DESC`, s.now().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		sess, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

// Revoke stops a session immediately.
func (s *Store) Revoke(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE sessions SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, s.now().UnixMilli(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := s.Get(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// ErrLimit means a file would exceed the session's limits.
var ErrLimit = errors.New("transfer: limit reached")

// Reserve claims one file (upload) or one download. It fails if the
// session is revoked or its count is used up.
func (s *Store) Reserve(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE sessions SET files = files + 1
		WHERE id = ? AND revoked_at IS NULL AND files < max_files AND bytes < max_bytes`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrLimit
	}
	return nil
}

// Release undoes a Reserve for a file that was not saved.
func (s *Store) Release(ctx context.Context, id string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE sessions SET files = MAX(files - 1, 0) WHERE id = ?`, id)
}

// Record adds a transferred file's bytes to the session and logs it.
func (s *Store) Record(ctx context.Context, sess Session, name string, size int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET bytes = bytes + ? WHERE id = ?`, size, sess.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO activity (at, session_id, kind, folder, name, size, actor) VALUES (?,?,?,?,?,?,?)`,
		s.now().UnixMilli(), sess.ID, string(sess.Kind), sess.Folder, name, size, sess.CreatedBy); err != nil {
		return err
	}
	return tx.Commit()
}

// Remaining returns how many more bytes the session accepts.
func (s *Store) Remaining(ctx context.Context, id string) (int64, error) {
	var max, used int64
	err := s.db.QueryRowContext(ctx, `SELECT max_bytes, bytes FROM sessions WHERE id = ?`, id).Scan(&max, &used)
	if err != nil {
		return 0, err
	}
	if used >= max {
		return 0, nil
	}
	return max - used, nil
}

// Item is one logged transfer.
type Item struct {
	At     time.Time `json:"at"`
	Kind   Kind      `json:"kind"`
	Folder string    `json:"folder"`
	Name   string    `json:"name"`
	Size   int64     `json:"size"`
	Actor  string    `json:"actor"`
}

// Received lists files transferred through one session, oldest first.
func (s *Store) Received(ctx context.Context, sessionID string) ([]Item, error) {
	return s.items(ctx, `SELECT at, kind, folder, name, size, actor FROM activity WHERE session_id = ? ORDER BY id`, sessionID)
}

// Recent lists the latest transfers across all sessions.
func (s *Store) Recent(ctx context.Context, limit int) ([]Item, error) {
	return s.items(ctx, `SELECT at, kind, folder, name, size, actor FROM activity ORDER BY id DESC LIMIT ?`, limit)
}

func (s *Store) items(ctx context.Context, q string, args ...any) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		var it Item
		var at int64
		var kind string
		if err := rows.Scan(&at, &kind, &it.Folder, &it.Name, &it.Size, &it.Actor); err != nil {
			return nil, err
		}
		it.At, it.Kind = time.UnixMilli(at), Kind(kind)
		out = append(out, it)
	}
	return out, rows.Err()
}

// Prune deletes sessions that ended more than a week ago and activity
// older than 90 days.
func (s *Store) Prune(ctx context.Context) error {
	now := s.now()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, now.Add(-7*24*time.Hour).UnixMilli()); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM activity WHERE at < ?`, now.Add(-90*24*time.Hour).UnixMilli())
	return err
}
