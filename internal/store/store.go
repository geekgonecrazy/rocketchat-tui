// Package store is the local SQLite cache. Everything the UI renders is read
// from here, so a launch with no network still shows the last known state.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver: no cgo, so cross-compiles cleanly
)

// Store owns the SQLite connection. Methods are safe for concurrent use.
type Store struct {
	db *sql.DB

	// selfID lets read paths flag a message as the local user's without every
	// caller threading the id through.
	selfID atomic.Value
}

// Open opens (creating if needed) the cache database at path and applies
// migrations.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("store: create %s: %w", dir, err)
		}
	}

	// WAL keeps the realtime writer from blocking UI reads; busy_timeout covers
	// the brief overlap when both hit the same page.
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// modernc's driver is happiest with a single writer; reads are fast enough
	// that serializing them keeps the code free of retry loops.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: ping %s: %w", path, err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// Reset drops all cached rows, used when switching accounts or servers so one
// account never sees another's history.
func (s *Store) Reset() error {
	_, err := s.db.Exec(`
		DELETE FROM messages;
		DELETE FROM subscriptions;
		DELETE FROM rooms;
		DELETE FROM room_history;
		DELETE FROM meta;
	`)
	if err != nil {
		return fmt.Errorf("store: reset: %w", err)
	}
	return nil
}

// ---- meta -------------------------------------------------------------------

// Meta reads a scalar cache value, returning "" when absent.
func (s *Store) Meta(key string) (string, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: read meta %q: %w", key, err)
	}
	return value, nil
}

// SetMeta writes a scalar cache value.
func (s *Store) SetMeta(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	if err != nil {
		return fmt.Errorf("store: write meta %q: %w", key, err)
	}
	return nil
}

// MetaTime reads a timestamp cache value, e.g. a sync cursor.
func (s *Store) MetaTime(key string) (time.Time, error) {
	raw, err := s.Meta(key)
	if err != nil || raw == "" {
		return time.Time{}, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, nil // treat a corrupt cursor as "never synced"
	}
	return parsed, nil
}

// SetMetaTime writes a timestamp cache value.
func (s *Store) SetMetaTime(key string, t time.Time) error {
	if t.IsZero() {
		return s.SetMeta(key, "")
	}
	return s.SetMeta(key, t.UTC().Format(time.RFC3339Nano))
}

// ---- helpers ----------------------------------------------------------------

// withTx runs fn inside a transaction, rolling back on error.
func (s *Store) withTx(fn func(*sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// millis converts a time to the integer encoding used in every timestamp
// column. Zero times become 0 so they sort before real values.
func millis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().UnixMilli()
}

// fromMillis is the inverse of millis.
func fromMillis(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
