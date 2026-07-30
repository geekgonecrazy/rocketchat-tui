package rocket

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// MaxDownloadBytes caps an attachment fetch. A terminal is never going to draw
// something this large, and the whole body is held in memory, so a hostile or
// misconfigured server should not be able to exhaust it.
const MaxDownloadBytes = 32 << 20

// File is a downloaded attachment body plus what the server said it is.
type File struct {
	Data []byte
	MIME string
}

// Download fetches a file the server hosts. Attachments carry a server-relative
// ref like "/file-upload/<id>/name.png", which uploads require credentials to
// read; ref may also be absolute, which is what an unfurled link preview
// carries.
//
// Credentials go out only when ref resolves onto the server's own host. An
// attachment URL is server-supplied data and can point anywhere, so sending the
// session token blindly would hand it to whatever host the message names.
func (c *Client) Download(ctx context.Context, ref string) (File, error) {
	target, sameHost, err := c.resolveRef(ref)
	if err != nil {
		return File{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return File{}, fmt.Errorf("rocket: build download request: %w", err)
	}
	if sameHost {
		creds := c.Credentials()
		if !creds.Valid() {
			return File{}, fmt.Errorf("rocket: downloading %s requires authentication", ref)
		}
		req.Header.Set("X-Auth-Token", creds.AuthToken)
		req.Header.Set("X-User-Id", creds.UserID)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return File{}, fmt.Errorf("rocket: download %s: %w", ref, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return File{}, fmt.Errorf("rocket: download %s: server said %s", ref, resp.Status)
	}

	// Read one byte past the cap so an oversized body is reported rather than
	// silently truncated — a half-read image decodes to garbage or not at all.
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxDownloadBytes+1))
	if err != nil {
		return File{}, fmt.Errorf("rocket: download %s: read body: %w", ref, err)
	}
	if len(data) > MaxDownloadBytes {
		return File{}, fmt.Errorf("rocket: %s is larger than %d MB", ref, MaxDownloadBytes>>20)
	}

	mime := resp.Header.Get("Content-Type")
	if cut := strings.IndexByte(mime, ';'); cut >= 0 {
		mime = strings.TrimSpace(mime[:cut])
	}
	return File{Data: data, MIME: mime}, nil
}

// resolveRef turns an attachment reference into an absolute URL, and reports
// whether it landed on the server we are logged in to.
func (c *Client) resolveRef(ref string) (target string, sameHost bool, err error) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return "", false, fmt.Errorf("rocket: empty download reference")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", false, fmt.Errorf("rocket: parse download reference %q: %w", ref, err)
	}

	resolved := c.baseURL.ResolveReference(parsed)
	switch resolved.Scheme {
	case "http", "https":
	default:
		return "", false, fmt.Errorf("rocket: refusing to download %q: unsupported scheme", ref)
	}
	return resolved.String(), strings.EqualFold(resolved.Host, c.baseURL.Host), nil
}
