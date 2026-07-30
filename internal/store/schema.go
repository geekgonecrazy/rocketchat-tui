package store

import "fmt"

// migrations are applied in order; user_version records how far we got, so
// adding a statement to the end is the only change ever needed.
var migrations = []string{
	`
	CREATE TABLE IF NOT EXISTS meta (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS rooms (
		id              TEXT PRIMARY KEY,
		type            TEXT NOT NULL DEFAULT '',
		name            TEXT NOT NULL DEFAULT '',
		display_name    TEXT NOT NULL DEFAULT '',
		topic           TEXT NOT NULL DEFAULT '',
		team_id         TEXT NOT NULL DEFAULT '',
		team_main       INTEGER NOT NULL DEFAULT 0,
		parent_room_id  TEXT NOT NULL DEFAULT '',
		last_message_at INTEGER NOT NULL DEFAULT 0,
		updated_at      INTEGER NOT NULL DEFAULT 0,
		user_count      INTEGER NOT NULL DEFAULT 0,
		read_only       INTEGER NOT NULL DEFAULT 0,
		archived        INTEGER NOT NULL DEFAULT 0
	);

	CREATE TABLE IF NOT EXISTS subscriptions (
		room_id        TEXT PRIMARY KEY,
		id             TEXT NOT NULL DEFAULT '',
		type           TEXT NOT NULL DEFAULT '',
		name           TEXT NOT NULL DEFAULT '',
		display_name   TEXT NOT NULL DEFAULT '',
		open           INTEGER NOT NULL DEFAULT 1,
		alert          INTEGER NOT NULL DEFAULT 0,
		unread         INTEGER NOT NULL DEFAULT 0,
		user_mentions  INTEGER NOT NULL DEFAULT 0,
		group_mentions INTEGER NOT NULL DEFAULT 0,
		favorite       INTEGER NOT NULL DEFAULT 0,
		team_id        TEXT NOT NULL DEFAULT '',
		team_main      INTEGER NOT NULL DEFAULT 0,
		parent_room_id TEXT NOT NULL DEFAULT '',
		last_seen_at   INTEGER NOT NULL DEFAULT 0,
		updated_at     INTEGER NOT NULL DEFAULT 0
	);

	CREATE TABLE IF NOT EXISTS messages (
		id             TEXT PRIMARY KEY,
		room_id        TEXT NOT NULL,
		thread_id      TEXT NOT NULL DEFAULT '',
		ts             INTEGER NOT NULL DEFAULT 0,
		updated_at     INTEGER NOT NULL DEFAULT 0,
		edited_at      INTEGER NOT NULL DEFAULT 0,
		user_id        TEXT NOT NULL DEFAULT '',
		username       TEXT NOT NULL DEFAULT '',
		author         TEXT NOT NULL DEFAULT '',
		text           TEXT NOT NULL DEFAULT '',
		system_type    TEXT NOT NULL DEFAULT '',
		thread_count   INTEGER NOT NULL DEFAULT 0,
		thread_last_at INTEGER NOT NULL DEFAULT 0,
		show_in_parent INTEGER NOT NULL DEFAULT 0,
		reactions      TEXT NOT NULL DEFAULT '',
		attachments    TEXT NOT NULL DEFAULT ''
	);

	CREATE INDEX IF NOT EXISTS messages_room_ts  ON messages(room_id, ts);
	CREATE INDEX IF NOT EXISTS messages_thread   ON messages(thread_id, ts);
	CREATE INDEX IF NOT EXISTS messages_parents  ON messages(room_id, thread_count, thread_last_at);

	-- Tracks how far back history has been fetched, so paging older knows
	-- whether to hit the network or serve from cache.
	CREATE TABLE IF NOT EXISTS room_history (
		room_id     TEXT PRIMARY KEY,
		oldest_ts   INTEGER NOT NULL DEFAULT 0,
		newest_ts   INTEGER NOT NULL DEFAULT 0,
		reached_end INTEGER NOT NULL DEFAULT 0,
		synced_at   INTEGER NOT NULL DEFAULT 0
	);
	`,
	-- Who can be mentioned in a room. Cached so the composer's @ completer has
	-- candidates to offer before — or without — a round trip.
	CREATE TABLE IF NOT EXISTS room_members (
		room_id  TEXT NOT NULL,
		username TEXT NOT NULL,
		name     TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (room_id, username)
	);
	`,
	`
	-- Who can be mentioned in a room. Cached so the composer's @ completer has
	-- candidates to offer before — or without — a round trip.
	CREATE TABLE IF NOT EXISTS room_members (
		room_id  TEXT NOT NULL,
		username TEXT NOT NULL,
		name     TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (room_id, username)
	);
	`,
}

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("store: read schema version: %w", err)
	}
	for i := version; i < len(migrations); i++ {
		if _, err := s.db.Exec(migrations[i]); err != nil {
			return fmt.Errorf("store: migration %d: %w", i+1, err)
		}
		// PRAGMA does not accept placeholders.
		if _, err := s.db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			return fmt.Errorf("store: bump schema version to %d: %w", i+1, err)
		}
	}
	return nil
}
