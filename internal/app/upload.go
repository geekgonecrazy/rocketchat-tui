package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
)

// Upload is a local file queued to go out with the next message.
//
// It is resolved when the user picks it rather than when the message is sent,
// so a path that does not exist is reported into the composer they are still
// looking at instead of surfacing as a failure moments after they hit enter.
type Upload struct {
	Path string
	Name string
	MIME string
	Size int64
}

// NewUpload resolves a path into a queued file, or explains why it cannot be
// one. Leading "~" is expanded, since the composer is where a user types paths
// and a shell is not there to do it for them.
func NewUpload(path string) (Upload, error) {
	resolved, err := ExpandPath(path)
	if err != nil {
		return Upload{}, err
	}
	if resolved == "" {
		return Upload{}, errors.New("give a path to a file")
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Upload{}, fmt.Errorf("no such file: %s", path)
		}
		return Upload{}, fmt.Errorf("cannot read %s: %w", path, err)
	}
	if info.IsDir() {
		return Upload{}, fmt.Errorf("%s is a directory", filepath.Base(resolved))
	}
	if !info.Mode().IsRegular() {
		return Upload{}, fmt.Errorf("%s is not a regular file", filepath.Base(resolved))
	}

	mime, err := rocket.DetectMIME(resolved)
	if err != nil {
		return Upload{}, err
	}
	return Upload{
		Path: resolved,
		Name: filepath.Base(resolved),
		MIME: mime,
		Size: info.Size(),
	}, nil
}

// IsImage reports whether this file will arrive as something the timeline can
// draw, which is what lets the composer mark it as a picture rather than a
// generic file.
func (u Upload) IsImage() bool { return strings.HasPrefix(u.MIME, "image/") }

// ExpandPath resolves a leading "~" against the user's home directory. Anything
// else is returned trimmed and otherwise untouched, including relative paths:
// resolving those is the process's working directory's job, not ours.
func ExpandPath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed != "~" && !strings.HasPrefix(trimmed, "~/") {
		return trimmed, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot find your home directory: %w", err)
	}
	if trimmed == "~" {
		return home, nil
	}
	return filepath.Join(home, trimmed[2:]), nil
}

// sendUploads posts each queued file in turn and returns a single error
// describing whatever failed.
//
// Files go out one at a time and in order, because they are one message each
// and the room should show them in the order they were attached. Uploading them
// concurrently would be faster and would scramble that.
func (c *Core) sendUploads(ctx context.Context, roomID, threadID, text string, uploads []Upload) error {
	// pending is the message text, still looking for a file to ride on. It is
	// cleared by the first upload that *succeeds* rather than simply the first
	// one attempted: a rejected first file must not take the user's words with
	// it.
	pending := text

	var (
		failed   []string
		firstErr error
		resync   bool
	)

	for _, upload := range uploads {
		sent, err := c.client.Upload(ctx, rocket.UploadOptions{
			RoomID:   roomID,
			Path:     upload.Path,
			Filename: upload.Name,
			MIME:     upload.MIME,
			Text:     pending,
			ThreadID: threadID,
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			c.logger.Warn("upload", "file", upload.Name, "err", err)
			failed = append(failed, upload.Name)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}

		pending = ""
		if sent.ID != "" {
			if err := c.store.SaveMessages([]rocket.Message{sent}); err != nil {
				return err
			}
			continue
		}
		// Confirming an upload is not obliged to echo the stored message back.
		// Caching a row with no id would be worse than not caching one, so the
		// room is asked what it now holds instead.
		resync = true
	}

	if pending != "" {
		// Every file was refused and there is still a message to deliver. Posting
		// it plainly is a visible half-result, but the alternative is discarding
		// something the user wrote and watched disappear from the composer. The
		// failure notice below says which files did not make it.
		if _, err := c.client.Send(ctx, rocket.SendOptions{
			RoomID:   roomID,
			Text:     pending,
			ThreadID: threadID,
		}); err != nil {
			c.logger.Warn("post message after failed uploads", "err", err)
		} else {
			resync = true
		}
	}

	c.enqueue(func(c *Core) {
		if resync && c.currentRoom == roomID {
			c.catchUpRoom(roomID)
		}
		c.refreshAfterSend(roomID, threadID)
	})

	if len(failed) > 0 {
		// One reason for however many files: they almost always fail for the same
		// cause — type not on the server's whitelist, over its size limit, no
		// permission — and the status bar has room for one sentence.
		return fmt.Errorf("could not upload %s: %w", englishList(failed), firstErr)
	}
	return nil
}

// englishList joins names the way a sentence would.
func englishList(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}
