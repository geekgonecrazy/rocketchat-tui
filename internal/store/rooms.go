package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
)

const upsertRoomSQL = `
INSERT INTO rooms (
	id, type, name, display_name, topic, team_id, team_main, parent_room_id,
	last_message_at, updated_at, user_count, read_only, archived
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
	type            = excluded.type,
	name            = excluded.name,
	display_name    = excluded.display_name,
	topic           = excluded.topic,
	team_id         = excluded.team_id,
	team_main       = excluded.team_main,
	parent_room_id  = excluded.parent_room_id,
	last_message_at = MAX(rooms.last_message_at, excluded.last_message_at),
	updated_at      = excluded.updated_at,
	user_count      = excluded.user_count,
	read_only       = excluded.read_only,
	archived        = excluded.archived`

// SaveRooms upserts room metadata.
func (s *Store) SaveRooms(rooms []rocket.Room) error {
	if len(rooms) == 0 {
		return nil
	}
	return s.withTx(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(upsertRoomSQL)
		if err != nil {
			return fmt.Errorf("store: prepare room upsert: %w", err)
		}
		defer stmt.Close()

		for _, room := range rooms {
			var lastMessage time.Time
			if room.LastMessage != nil {
				lastMessage = room.LastMessage.Time
			}
			_, err := stmt.Exec(
				room.ID, room.Type, room.Name, firstNonEmpty(room.DisplayName, room.Name), room.Topic,
				room.TeamID, boolToInt(room.TeamMain), room.ParentRoomID,
				millis(lastMessage), millis(room.UpdatedAt.Time), room.UserCount,
				boolToInt(room.ReadOnly), boolToInt(room.Archived),
			)
			if err != nil {
				return fmt.Errorf("store: save room %s: %w", room.ID, err)
			}
		}
		return nil
	})
}

const upsertSubscriptionSQL = `
INSERT INTO subscriptions (
	room_id, id, type, name, display_name, open, alert, unread,
	user_mentions, group_mentions, favorite, team_id, team_main,
	parent_room_id, last_seen_at, updated_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(room_id) DO UPDATE SET
	id             = excluded.id,
	type           = excluded.type,
	name           = excluded.name,
	display_name   = excluded.display_name,
	open           = excluded.open,
	alert          = excluded.alert,
	unread         = excluded.unread,
	user_mentions  = excluded.user_mentions,
	group_mentions = excluded.group_mentions,
	favorite       = excluded.favorite,
	team_id        = excluded.team_id,
	team_main      = excluded.team_main,
	parent_room_id = excluded.parent_room_id,
	last_seen_at   = MAX(subscriptions.last_seen_at, excluded.last_seen_at),
	updated_at     = excluded.updated_at`

// SaveSubscriptions upserts the per-user room state that drives unreads.
func (s *Store) SaveSubscriptions(subs []rocket.Subscription) error {
	if len(subs) == 0 {
		return nil
	}
	return s.withTx(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(upsertSubscriptionSQL)
		if err != nil {
			return fmt.Errorf("store: prepare subscription upsert: %w", err)
		}
		defer stmt.Close()

		for _, sub := range subs {
			var lastSeen time.Time
			if sub.LastSeen != nil {
				lastSeen = sub.LastSeen.Time
			}
			_, err := stmt.Exec(
				sub.RoomID, sub.ID, sub.Type, sub.Name, firstNonEmpty(sub.DisplayName, sub.Name),
				boolToInt(sub.Open), boolToInt(sub.Alert), sub.Unread,
				sub.UserMentions, sub.GroupMentions, boolToInt(sub.Favorite),
				sub.TeamID, boolToInt(sub.TeamMain), sub.ParentRoomID,
				millis(lastSeen), millis(sub.UpdatedAt.Time),
			)
			if err != nil {
				return fmt.Errorf("store: save subscription %s: %w", sub.RoomID, err)
			}
		}
		return nil
	})
}

// DeleteSubscription removes a room the user has left.
func (s *Store) DeleteSubscription(roomID string) error {
	_, err := s.db.Exec(`DELETE FROM subscriptions WHERE room_id = ?`, roomID)
	if err != nil {
		return fmt.Errorf("store: delete subscription %s: %w", roomID, err)
	}
	return nil
}

// The sidebar reads room identity from whichever source has it: rooms.get has
// topics and types, subscriptions.get has names for DMs and all unread state.
//
// Ordering deliberately uses message activity only — the room's `lm` and the
// newest message actually cached — and never the subscription's `_updatedAt`.
// The server bumps that field on any subscription change, marking a room read
// included, so using it made a room jump to the top of the sidebar simply
// because the user opened it. The subquery covers messages that have arrived
// over the websocket but are not yet reflected in rooms.get; it is indexed by
// (room_id, ts), so it is a cheap lookup per row.
const selectRoomsSQL = `
SELECT
	s.room_id,
	COALESCE(NULLIF(r.type, ''), s.type),
	COALESCE(NULLIF(r.name, ''), s.name),
	COALESCE(NULLIF(r.display_name, ''), NULLIF(s.display_name, ''), NULLIF(r.name, ''), s.name),
	COALESCE(r.topic, ''),
	COALESCE(NULLIF(r.team_id, ''), s.team_id),
	MAX(COALESCE(r.team_main, 0), s.team_main),
	COALESCE(NULLIF(r.parent_room_id, ''), s.parent_room_id),
	s.unread, s.user_mentions, s.group_mentions, s.alert, s.open, s.favorite,
	MAX(
		COALESCE(r.last_message_at, 0),
		COALESCE((SELECT MAX(m.ts) FROM messages m WHERE m.room_id = s.room_id), 0)
	),
	s.last_seen_at,
	COALESCE(r.read_only, 0),
	COALESCE(r.archived, 0),
	COALESCE(r.user_count, 0)
FROM subscriptions s
LEFT JOIN rooms r ON r.id = s.room_id`

// Rooms returns every open subscription as a sidebar row, already sorted.
func (s *Store) Rooms() ([]model.Room, error) {
	rows, err := s.db.Query(selectRoomsSQL + ` WHERE s.open = 1`)
	if err != nil {
		return nil, fmt.Errorf("store: list rooms: %w", err)
	}
	defer rows.Close()

	var rooms []model.Room
	for rows.Next() {
		room, err := scanRoom(rows)
		if err != nil {
			return nil, err
		}
		rooms = append(rooms, room)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list rooms: %w", err)
	}
	model.SortRooms(rooms)
	return rooms, nil
}

// Room returns one room, reporting false when it is not cached.
func (s *Store) Room(roomID string) (model.Room, bool, error) {
	row := s.db.QueryRow(selectRoomsSQL+` WHERE s.room_id = ?`, roomID)
	room, err := scanRoom(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Room{}, false, nil
	}
	if err != nil {
		return model.Room{}, false, err
	}
	return room, true, nil
}

// scanner covers both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanRoom(row scanner) (model.Room, error) {
	var (
		room                              model.Room
		roomType                          string
		teamMain, alert, open, favorite   int
		readOnly, archived                int
		lastMessageMillis, lastSeenMillis int64
	)
	err := row.Scan(
		&room.ID, &roomType, &room.Name, &room.DisplayName, &room.Topic,
		&room.TeamID, &teamMain, &room.ParentRoomID,
		&room.Unread, &room.UserMentions, &room.GroupMentions, &alert, &open, &favorite,
		&lastMessageMillis, &lastSeenMillis, &readOnly, &archived, &room.UserCount,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Room{}, err
		}
		return model.Room{}, fmt.Errorf("store: scan room: %w", err)
	}
	room.TeamMain = teamMain == 1
	room.Alert = alert == 1
	room.Open = open == 1
	room.Favorite = favorite == 1
	room.ReadOnly = readOnly == 1
	room.Archived = archived == 1
	room.LastMessageAt = fromMillis(lastMessageMillis)
	room.LastSeenAt = fromMillis(lastSeenMillis)
	room.Kind = model.RoomKind(roomType, room.TeamMain, room.ParentRoomID)
	return room, nil
}

// RoomType returns the raw Rocket.Chat type letter, which the REST history
// endpoints need in order to pick a path.
func (s *Store) RoomType(roomID string) (string, error) {
	var roomType string
	err := s.db.QueryRow(`
		SELECT COALESCE(NULLIF(r.type, ''), s.type)
		FROM subscriptions s LEFT JOIN rooms r ON r.id = s.room_id
		WHERE s.room_id = ?`, roomID).Scan(&roomType)
	if errors.Is(err, sql.ErrNoRows) {
		// Fall back to the rooms table for a room we know but do not subscribe to.
		err = s.db.QueryRow(`SELECT type FROM rooms WHERE id = ?`, roomID).Scan(&roomType)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
	}
	if err != nil {
		return "", fmt.Errorf("store: room type %s: %w", roomID, err)
	}
	return roomType, nil
}

// ClearUnread zeroes a room's unread state locally, mirroring what the server
// does when we mark it read.
func (s *Store) ClearUnread(roomID string, lastSeen time.Time) error {
	_, err := s.db.Exec(`
		UPDATE subscriptions
		SET unread = 0, user_mentions = 0, group_mentions = 0, alert = 0,
		    last_seen_at = MAX(last_seen_at, ?)
		WHERE room_id = ?`, millis(lastSeen), roomID)
	if err != nil {
		return fmt.Errorf("store: clear unread %s: %w", roomID, err)
	}
	return nil
}

// LastSeen returns the room's last-seen marker, which positions the unread
// divider.
func (s *Store) LastSeen(roomID string) (time.Time, error) {
	var ms int64
	err := s.db.QueryRow(`SELECT last_seen_at FROM subscriptions WHERE room_id = ?`, roomID).Scan(&ms)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("store: last seen %s: %w", roomID, err)
	}
	return fromMillis(ms), nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
