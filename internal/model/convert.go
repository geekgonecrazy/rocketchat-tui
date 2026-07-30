package model

import (
	"sort"
	"time"

	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
)

// RoomKind resolves the UI-facing kind from the wire fields. Order matters:
// a discussion is also a c/p room, and a team main room is also a channel.
func RoomKind(roomType string, teamMain bool, parentRoomID string) Kind {
	switch {
	case parentRoomID != "":
		return KindDiscussion
	case teamMain:
		return KindTeam
	case roomType == rocket.RoomTypeDirect:
		return KindDirect
	case roomType == rocket.RoomTypePrivate:
		return KindPrivate
	case roomType == rocket.RoomTypeLive:
		return KindOmnichannel
	default:
		return KindChannel
	}
}

// FromRocketMessage converts an SDK message into the view type. selfID marks
// messages authored by the logged-in user, and selfUsername marks reactions the
// user has already made — reactions identify people by username, not id.
func FromRocketMessage(src rocket.Message, selfID, selfUsername string) Message {
	msg := Message{
		ID:           src.ID,
		RoomID:       src.RoomID,
		ThreadID:     src.ThreadParentID,
		Username:     src.User.Username,
		Author:       src.User.Name,
		Text:         src.Msg,
		At:           src.Timestamp.Time,
		SystemType:   src.Type,
		ThreadCount:  src.ThreadCount,
		ShowInParent: src.ShowInParent,
		Own:          selfID != "" && src.User.ID == selfID,
	}
	if msg.Author == "" {
		msg.Author = src.User.Username
	}
	if src.EditedAt != nil {
		msg.EditedAt = src.EditedAt.Time
	}
	if src.ThreadLastAt != nil {
		msg.ThreadLastAt = src.ThreadLastAt.Time
	}
	for shortcode, reaction := range src.Reactions {
		msg.Reactions = append(msg.Reactions, Reaction{
			Emoji:     shortcode,
			Usernames: reaction.Usernames,
			Mine:      containsName(reaction.Usernames, selfUsername),
		})
	}
	sort.Slice(msg.Reactions, func(i, j int) bool { return msg.Reactions[i].Emoji < msg.Reactions[j].Emoji })

	for _, attachment := range src.Attachments {
		converted := Attachment{
			Title: firstNonEmpty(attachment.Title, attachment.AuthorName),
			Text:  firstNonEmpty(attachment.Text, attachment.Description),
			Link:  firstNonEmpty(attachment.TitleLink, attachment.ImageURL),
		}
		if converted.Title == "" && converted.Text == "" && converted.Link == "" {
			continue
		}
		msg.Attachments = append(msg.Attachments, converted)
	}
	return msg
}

// MergeRoom folds room metadata and the user's subscription into one view row.
// Either side may be missing: rooms.get and subscriptions.get can disagree
// briefly, and realtime delivers them independently.
func MergeRoom(room *rocket.Room, sub *rocket.Subscription) Room {
	var out Room
	if room != nil {
		out.ID = room.ID
		out.Name = room.Name
		out.DisplayName = firstNonEmpty(room.DisplayName, room.Name)
		out.Topic = room.Topic
		out.TeamID = room.TeamID
		out.TeamMain = room.TeamMain
		out.ParentRoomID = room.ParentRoomID
		out.ReadOnly = room.ReadOnly
		out.Archived = room.Archived
		out.UserCount = room.UserCount
		out.Kind = RoomKind(room.Type, room.TeamMain, room.ParentRoomID)
		if room.LastMessage != nil {
			out.LastMessageAt = room.LastMessage.Time
		}
	}
	if sub != nil {
		out.ID = sub.RoomID
		if out.DisplayName == "" {
			out.DisplayName = firstNonEmpty(sub.DisplayName, sub.Name)
		}
		if out.Name == "" {
			out.Name = sub.Name
		}
		out.Unread = sub.Unread
		out.UserMentions = sub.UserMentions
		out.GroupMentions = sub.GroupMentions
		out.Alert = sub.Alert
		out.Open = sub.Open
		out.Favorite = sub.Favorite
		if sub.LastSeen != nil {
			out.LastSeenAt = sub.LastSeen.Time
		}
		if out.TeamID == "" {
			out.TeamID = sub.TeamID
		}
		if !out.TeamMain {
			out.TeamMain = sub.TeamMain
		}
		if out.ParentRoomID == "" {
			out.ParentRoomID = sub.ParentRoomID
		}
		if room == nil {
			out.Kind = RoomKind(sub.Type, sub.TeamMain, sub.ParentRoomID)
		}
	}
	return out
}

// LatestTime returns the newest of the given times, ignoring zero values.
func LatestTime(times ...time.Time) time.Time {
	var latest time.Time
	for _, t := range times {
		if t.After(latest) {
			latest = t
		}
	}
	return latest
}

// containsName reports whether a username appears in a reaction's list.
func containsName(names []string, want string) bool {
	if want == "" {
		return false
	}
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
