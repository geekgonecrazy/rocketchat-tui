package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
)

// FetchAttachment pulls an attachment down to a local file and reports the path
// back as an AttachmentFetched event.
//
// The file lands in the user's cache directory keyed by its source URL, so
// re-opening the same image is a stat rather than a round trip, and so "open
// externally" has a real path to hand the desktop.
//
// messageID and index identify which attachment this is, because the UI may
// have moved on — or opened a different message — by the time the bytes land.
func (c *Core) FetchAttachment(messageID string, index int, attachment model.Attachment) {
	c.enqueue(func(c *Core) {
		if attachment.Source == "" {
			c.emit(AttachmentFetched{
				MessageID:  messageID,
				Index:      index,
				Attachment: attachment,
				Err:        errors.New("this attachment has nothing to download"),
			})
			return
		}
		c.background(func(ctx context.Context) error {
			path, mime, err := c.downloadAttachment(ctx, attachment)
			if err != nil {
				c.logger.Warn("fetch attachment", "source", attachment.Source, "err", err)
			}
			c.emit(AttachmentFetched{
				MessageID:  messageID,
				Index:      index,
				Attachment: attachment,
				Path:       path,
				MIME:       mime,
				Err:        err,
			})
			// The error is already on its way to the UI in the event above;
			// returning it too would report the same failure twice.
			return nil
		})
	})
}

// downloadAttachment returns the local path of the attachment, fetching it only
// if it is not already cached.
func (c *Core) downloadAttachment(ctx context.Context, attachment model.Attachment) (string, string, error) {
	dir, err := attachmentCacheDir()
	if err != nil {
		return "", "", err
	}
	path := filepath.Join(dir, cacheName(attachment))

	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		return path, attachment.MIME, nil
	}

	file, err := c.client.Download(ctx, attachment.Source)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("app: create attachment cache: %w", err)
	}
	// Write through a temporary file so an interrupted download cannot leave a
	// truncated body behind that the stat above would later treat as a hit.
	temp, err := os.CreateTemp(dir, ".partial-*")
	if err != nil {
		return "", "", fmt.Errorf("app: create attachment cache file: %w", err)
	}
	tempName := temp.Name()
	if _, err := temp.Write(file.Data); err != nil {
		temp.Close()
		os.Remove(tempName)
		return "", "", fmt.Errorf("app: write attachment cache: %w", err)
	}
	if err := temp.Close(); err != nil {
		os.Remove(tempName)
		return "", "", fmt.Errorf("app: write attachment cache: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		os.Remove(tempName)
		return "", "", fmt.Errorf("app: publish attachment cache: %w", err)
	}

	mime := file.MIME
	if mime == "" {
		mime = attachment.MIME
	}
	return path, mime, nil
}

// attachmentCacheDir is where downloaded attachments live. Cache, not data:
// every file here can be fetched again, so it is safe for the OS to reclaim.
func attachmentCacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("app: locate cache directory: %w", err)
	}
	return filepath.Join(base, "rctui", "attachments"), nil
}

// cacheName keys the cached file by its source so two uploads that share a
// filename cannot collide, while keeping the real name on the end so the
// directory stays readable and "open externally" hands the desktop a name it
// can pick a program from.
func cacheName(attachment model.Attachment) string {
	sum := sha256.Sum256([]byte(attachment.Source))
	return hex.EncodeToString(sum[:8]) + "-" + attachment.Filename()
}
