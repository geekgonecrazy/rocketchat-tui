package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
)

// SetIdentity records the logged-in user, used to flag own messages (by id) and
// own reactions (by username, which is how reactions identify people).
func (s *Store) SetIdentity(userID, username string) {
	s.selfID.Store(userID)
	s.selfUsername.Store(username)
}

func (s *Store) identity() string {
	if v, ok := s.selfID.Load().(string); ok {
		return v
	}
	return ""
}

func (s *Store) identityName() string {
	if v, ok := s.selfUsername.Load().(string); ok {
		return v
	}
	return ""
}

const upsertMessageSQL = `
INSERT INTO messages (
	id, room_id, thread_id, ts, updated_at, edited_at, user_id, username, author,
	text, system_type, thread_count, thread_last_at, show_in_parent, reactions, attachments
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
	room_id        = excluded.room_id,
	thread_id      = excluded.thread_id,
	ts             = excluded.ts,
	updated_at     = excluded.updated_at,
	edited_at      = excluded.edited_at,
	user_id        = excluded.user_id,
	username       = excluded.username,
	author         = excluded.author,
	text           = excluded.text,
	system_type    = excluded.system_type,
	thread_count   = excluded.thread_count,
	thread_last_at = excluded.thread_last_at,
	show_in_parent = excluded.show_in_parent,
	reactions      = excluded.reactions,
	attachments    = excluded.attachments`

// SaveMessages upserts messages. Realtime and REST both land here, so the
// upsert is what makes duplicate delivery harmless.
func (s *Store) SaveMessages(messages []rocket.Message) error {
	if len(messages) == 0 {
		return nil
	}
	return s.withTx(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(upsertMessageSQL)
		if err != nil {
			return fmt.Errorf("store: prepare message upsert: %w", err)
		}
		defer stmt.Close()

		for _, msg := range messages {
			if msg.ID == "" || msg.RoomID == "" {
				continue
			}
			var editedAt, threadLastAt time.Time
			if msg.EditedAt != nil {
				editedAt = msg.EditedAt.Time
			}
			if msg.ThreadLastAt != nil {
				threadLastAt = msg.ThreadLastAt.Time
			}
			reactions, err := encodeJSON(msg.Reactions)
			if err != nil {
				return err
			}
			attachments, err := encodeJSON(msg.Attachments)
			if err != nil {
				return err
			}
			_, err = stmt.Exec(
				msg.ID, msg.RoomID, msg.ThreadParentID, millis(msg.Timestamp.Time),
				millis(msg.UpdatedAt.Time), millis(editedAt),
				msg.User.ID, msg.User.Username, firstNonEmpty(msg.User.Name, msg.User.Username),
				msg.Msg, msg.Type, msg.ThreadCount, millis(threadLastAt),
				boolToInt(msg.ShowInParent), reactions, attachments,
			)
			if err != nil {
				return fmt.Errorf("store: save message %s: %w", msg.ID, err)
			}
		}
		return nil
	})
}

// DeleteMessage removes a message the server says is gone.
func (s *Store) DeleteMessage(messageID string) error {
	_, err := s.db.Exec(`DELETE FROM messages WHERE id = ?`, messageID)
	if err != nil {
		return fmt.Errorf("store: delete message %s: %w", messageID, err)
	}
	return nil
}

const selectMessageColumns = `
	id, room_id, thread_id, ts, edited_at, user_id, username, author, text,
	system_type, thread_count, thread_last_at, show_in_parent, reactions, attachments`

// The main timeline hides thread replies, matching the web client: a reply only
// appears inline when its author ticked "also send to channel".
const timelineFilter = `(thread_id = '' OR show_in_parent = 1)`

// RoomTimeline returns the newest limit messages for a room, oldest first.
func (s *Store) RoomTimeline(roomID string, limit int) ([]model.Message, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`
		SELECT `+selectMessageColumns+`
		FROM messages
		WHERE room_id = ? AND `+timelineFilter+`
		ORDER BY ts DESC
		LIMIT ?`, roomID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: room timeline %s: %w", roomID, err)
	}
	messages, err := s.scanMessages(rows)
	if err != nil {
		return nil, err
	}
	reverse(messages)
	return messages, nil
}

// RoomTimelineBefore returns up to limit messages older than before, oldest
// first. Used for scroll-back paging.
func (s *Store) RoomTimelineBefore(roomID string, before time.Time, limit int) ([]model.Message, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`
		SELECT `+selectMessageColumns+`
		FROM messages
		WHERE room_id = ? AND ts < ? AND `+timelineFilter+`
		ORDER BY ts DESC
		LIMIT ?`, roomID, millis(before), limit)
	if err != nil {
		return nil, fmt.Errorf("store: room timeline before %s: %w", roomID, err)
	}
	messages, err := s.scanMessages(rows)
	if err != nil {
		return nil, err
	}
	reverse(messages)
	return messages, nil
}

// ThreadReplies returns the replies in a thread, oldest first.
func (s *Store) ThreadReplies(threadID string) ([]model.Message, error) {
	rows, err := s.db.Query(`
		SELECT `+selectMessageColumns+`
		FROM messages
		WHERE thread_id = ?
		ORDER BY ts ASC`, threadID)
	if err != nil {
		return nil, fmt.Errorf("store: thread replies %s: %w", threadID, err)
	}
	return s.scanMessages(rows)
}

// ThreadParents returns a room's thread parents, most recent activity first.
func (s *Store) ThreadParents(roomID string, limit int) ([]model.Message, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`
		SELECT `+selectMessageColumns+`
		FROM messages
		WHERE room_id = ? AND thread_count > 0
		ORDER BY MAX(thread_last_at, ts) DESC
		LIMIT ?`, roomID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: thread parents %s: %w", roomID, err)
	}
	return s.scanMessages(rows)
}

// Message returns one message by id, reporting false when it is not cached.
func (s *Store) Message(messageID string) (model.Message, bool, error) {
	row := s.db.QueryRow(`SELECT `+selectMessageColumns+` FROM messages WHERE id = ?`, messageID)
	msg, err := s.scanMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Message{}, false, nil
	}
	if err != nil {
		return model.Message{}, false, err
	}
	return msg, true, nil
}

// OldestTimestamp returns the oldest cached message time for a room, which is
// where the next history page starts.
func (s *Store) OldestTimestamp(roomID string) (time.Time, error) {
	var ms sql.NullInt64
	err := s.db.QueryRow(`SELECT MIN(ts) FROM messages WHERE room_id = ?`, roomID).Scan(&ms)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, fmt.Errorf("store: oldest timestamp %s: %w", roomID, err)
	}
	if !ms.Valid {
		return time.Time{}, nil
	}
	return fromMillis(ms.Int64), nil
}

// NewestTimestamp returns the newest cached message time for a room, used as the
// incremental sync cursor.
func (s *Store) NewestTimestamp(roomID string) (time.Time, error) {
	var ms sql.NullInt64
	err := s.db.QueryRow(`SELECT MAX(ts) FROM messages WHERE room_id = ?`, roomID).Scan(&ms)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, fmt.Errorf("store: newest timestamp %s: %w", roomID, err)
	}
	if !ms.Valid {
		return time.Time{}, nil
	}
	return fromMillis(ms.Int64), nil
}

// CountAfter returns how many messages in a room are newer than a point in
// time, which is how the unread divider is placed without trusting the server's
// counter alone.
func (s *Store) CountAfter(roomID string, after time.Time) (int, error) {
	if after.IsZero() {
		return 0, nil
	}
	var count int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM messages
		WHERE room_id = ? AND ts > ? AND `+timelineFilter, roomID, millis(after)).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("store: count after %s: %w", roomID, err)
	}
	return count, nil
}

// HistoryState records how much of a room's history is cached.
type HistoryState struct {
	OldestTS   time.Time
	NewestTS   time.Time
	ReachedEnd bool // no more history exists before OldestTS
	SyncedAt   time.Time
}

// HistoryState returns the cached paging state for a room.
func (s *Store) HistoryState(roomID string) (HistoryState, error) {
	var (
		state                    HistoryState
		oldest, newest, syncedAt int64
		reachedEnd               int
	)
	err := s.db.QueryRow(`
		SELECT oldest_ts, newest_ts, reached_end, synced_at
		FROM room_history WHERE room_id = ?`, roomID).Scan(&oldest, &newest, &reachedEnd, &syncedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return HistoryState{}, nil
	}
	if err != nil {
		return HistoryState{}, fmt.Errorf("store: history state %s: %w", roomID, err)
	}
	state.OldestTS = fromMillis(oldest)
	state.NewestTS = fromMillis(newest)
	state.ReachedEnd = reachedEnd == 1
	state.SyncedAt = fromMillis(syncedAt)
	return state, nil
}

// SaveHistoryState persists the paging state for a room.
func (s *Store) SaveHistoryState(roomID string, state HistoryState) error {
	_, err := s.db.Exec(`
		INSERT INTO room_history (room_id, oldest_ts, newest_ts, reached_end, synced_at)
		VALUES (?,?,?,?,?)
		ON CONFLICT(room_id) DO UPDATE SET
			oldest_ts   = CASE WHEN excluded.oldest_ts = 0 THEN room_history.oldest_ts
			                   WHEN room_history.oldest_ts = 0 THEN excluded.oldest_ts
			                   ELSE MIN(room_history.oldest_ts, excluded.oldest_ts) END,
			newest_ts   = MAX(room_history.newest_ts, excluded.newest_ts),
			reached_end = MAX(room_history.reached_end, excluded.reached_end),
			synced_at   = excluded.synced_at`,
		roomID, millis(state.OldestTS), millis(state.NewestTS),
		boolToInt(state.ReachedEnd), millis(state.SyncedAt))
	if err != nil {
		return fmt.Errorf("store: save history state %s: %w", roomID, err)
	}
	return nil
}

// ---- scanning ---------------------------------------------------------------

func (s *Store) scanMessages(rows *sql.Rows) ([]model.Message, error) {
	defer rows.Close()
	var messages []model.Message
	for rows.Next() {
		msg, err := s.scanMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: scan messages: %w", err)
	}
	return messages, nil
}

func (s *Store) scanMessage(row scanner) (model.Message, error) {
	var (
		msg                            model.Message
		userID                         string
		ts, editedAt, threadLastAt     int64
		showInParent                   int
		reactionsJSON, attachmentsJSON string
	)
	err := row.Scan(
		&msg.ID, &msg.RoomID, &msg.ThreadID, &ts, &editedAt, &userID,
		&msg.Username, &msg.Author, &msg.Text, &msg.SystemType,
		&msg.ThreadCount, &threadLastAt, &showInParent,
		&reactionsJSON, &attachmentsJSON,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Message{}, err
		}
		return model.Message{}, fmt.Errorf("store: scan message: %w", err)
	}
	msg.At = fromMillis(ts)
	msg.EditedAt = fromMillis(editedAt)
	msg.ThreadLastAt = fromMillis(threadLastAt)
	msg.ShowInParent = showInParent == 1
	msg.Own = userID != "" && userID == s.identity()

	if reactions, err := decodeReactions(reactionsJSON, s.identityName()); err == nil {
		msg.Reactions = reactions
	}
	if attachments, err := decodeAttachments(attachmentsJSON); err == nil {
		msg.Attachments = attachments
	}
	return msg, nil
}

func encodeJSON(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("store: encode json: %w", err)
	}
	if string(encoded) == "null" {
		return "", nil
	}
	return string(encoded), nil
}

func decodeReactions(raw, selfUsername string) ([]model.Reaction, error) {
	if raw == "" {
		return nil, nil
	}
	var wire map[string]rocket.Reaction
	if err := json.Unmarshal([]byte(raw), &wire); err != nil {
		return nil, err
	}
	reactions := make([]model.Reaction, 0, len(wire))
	for shortcode, reaction := range wire {
		mine := false
		for _, name := range reaction.Usernames {
			if name == selfUsername && selfUsername != "" {
				mine = true
				break
			}
		}
		reactions = append(reactions, model.Reaction{
			Emoji: shortcode, Usernames: reaction.Usernames, Mine: mine,
		})
	}
	// Deterministic order so the same message never renders two ways.
	for i := 1; i < len(reactions); i++ {
		for j := i; j > 0 && reactions[j].Emoji < reactions[j-1].Emoji; j-- {
			reactions[j], reactions[j-1] = reactions[j-1], reactions[j]
		}
	}
	return reactions, nil
}

func decodeAttachments(raw string) ([]model.Attachment, error) {
	if raw == "" {
		return nil, nil
	}
	var wire []rocket.Attachment
	if err := json.Unmarshal([]byte(raw), &wire); err != nil {
		return nil, err
	}
	var attachments []model.Attachment
	for _, attachment := range wire {
		converted := model.Attachment{
			Title: firstNonEmpty(attachment.Title, attachment.AuthorName),
			Text:  firstNonEmpty(attachment.Text, attachment.Description),
			Link:  firstNonEmpty(attachment.TitleLink, attachment.ImageURL),
		}
		if converted.Title == "" && converted.Text == "" && converted.Link == "" {
			continue
		}
		attachments = append(attachments, converted)
	}
	return attachments, nil
}

func reverse(messages []model.Message) {
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
}
