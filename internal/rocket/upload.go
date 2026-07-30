package rocket

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// UploadOptions describes one file being posted into a room.
//
// Text is the message the user wrote. Rocket.Chat attaches it to the file's own
// message rather than posting it separately, which is why a caller sending
// several files puts the text on exactly one of them.
type UploadOptions struct {
	RoomID string
	// Path is the local file to send. It is opened twice at most: once per
	// attempt, because a streamed body cannot be replayed for the fallback.
	Path string
	// Filename is what the room will show. Empty means the base of Path.
	Filename string
	// MIME overrides the detected type. Empty means detect it.
	MIME string

	Text              string
	ThreadID          string
	AlsoSendToChannel bool
}

// Upload posts a file to a room and returns the message the server created for
// it.
//
// Two server-side routes can do this. Current servers expect the file and the
// message to arrive separately — `rooms.media` takes the bytes and hands back a
// file id, `rooms.mediaConfirm` turns that id into a message — which is what the
// web client uses. Servers predating that route only have `rooms.upload`, which
// takes both at once. We try the newer pair and fall back on a 404, then
// remember the answer: the fallback costs one wasted round trip per session
// rather than one per file.
//
// The returned message may be zero even on success. `rooms.mediaConfirm` is not
// contractually obliged to echo the stored message back, and a caller that needs
// it in the timeline should be prepared to resync instead of trusting the reply.
func (c *Client) Upload(ctx context.Context, opts UploadOptions) (Message, error) {
	if opts.RoomID == "" {
		return Message{}, fmt.Errorf("rocket: upload requires a room id")
	}
	source, err := statUpload(opts.Path)
	if err != nil {
		return Message{}, err
	}
	if opts.Filename == "" {
		opts.Filename = source
	}
	if opts.MIME == "" {
		opts.MIME, err = DetectMIME(opts.Path)
		if err != nil {
			return Message{}, err
		}
	}

	if !c.usesLegacyUpload() {
		message, err := c.uploadViaMedia(ctx, opts)
		if err == nil {
			return message, nil
		}
		if !isMissingRoute(err) {
			return Message{}, err
		}
		// The server is old enough not to know the route at all. Nothing about
		// the file caused this, so every later upload can skip the attempt.
		c.markLegacyUpload()
	}
	return c.uploadViaLegacy(ctx, opts)
}

// uploadViaMedia is the two-step flow: bytes, then message.
func (c *Client) uploadViaMedia(ctx context.Context, opts UploadOptions) (Message, error) {
	var uploaded struct {
		File struct {
			ID string `json:"_id"`
		} `json:"file"`
	}
	err := c.postFile(ctx, "rooms.media/"+url.PathEscape(opts.RoomID), opts, nil, &uploaded)
	if err != nil {
		return Message{}, err
	}
	if uploaded.File.ID == "" {
		// Confirming needs an id. Failing here rather than posting to an empty
		// path keeps a malformed reply from turning into a confusing 404 that
		// would then be misread as "this server has no media route".
		return Message{}, fmt.Errorf("rocket: upload %s: server accepted the file but returned no id", opts.Filename)
	}

	payload := map[string]any{}
	if opts.Text != "" {
		payload["msg"] = opts.Text
	}
	if opts.ThreadID != "" {
		payload["tmid"] = opts.ThreadID
		if opts.AlsoSendToChannel {
			payload["tshow"] = true
		}
	}

	var resp struct {
		Message Message `json:"message"`
	}
	err = c.do(ctx, request{
		method: http.MethodPost,
		endpoint: "rooms.mediaConfirm/" + url.PathEscape(opts.RoomID) + "/" +
			url.PathEscape(uploaded.File.ID),
		body: payload,
	}, &resp)
	if err != nil {
		return Message{}, err
	}
	return resp.Message, nil
}

// uploadViaLegacy is the single-request flow older servers offer.
func (c *Client) uploadViaLegacy(ctx context.Context, opts UploadOptions) (Message, error) {
	fields := map[string]string{}
	if opts.Text != "" {
		fields["msg"] = opts.Text
	}
	if opts.ThreadID != "" {
		fields["tmid"] = opts.ThreadID
		if opts.AlsoSendToChannel {
			fields["tshow"] = "true"
		}
	}

	var resp struct {
		Message Message `json:"message"`
	}
	err := c.postFile(ctx, "rooms.upload/"+url.PathEscape(opts.RoomID), opts, fields, &resp)
	if err != nil {
		return Message{}, err
	}
	return resp.Message, nil
}

// postFile streams one multipart request: the file part, plus whatever extra
// text fields the endpoint wants.
//
// The body is piped straight off disk rather than assembled in memory. Uploads
// have no useful size ceiling — the server owns that policy — so buffering would
// mean holding an arbitrarily large file in RAM to send it.
func (c *Client) postFile(ctx context.Context, endpoint string, opts UploadOptions, fields map[string]string, out any) error {
	file, err := os.Open(opts.Path)
	if err != nil {
		return fmt.Errorf("rocket: upload %s: %w", opts.Filename, err)
	}

	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)

	go func() {
		// The goroutine owns the file, not this function: a server may answer
		// before it has read the whole body, in which case do() returns while
		// the copy is still running and a deferred close here would pull the
		// file out from under it.
		defer file.Close()
		// CloseWithError(nil) is CloseWithError(io.EOF), so a clean finish and a
		// failure both unblock the reader; the http client sees the error as a
		// request-body failure and abandons the call.
		writer.CloseWithError(writeUploadBody(form, file, opts, fields))
	}()

	err = c.do(ctx, request{
		method:      http.MethodPost,
		endpoint:    endpoint,
		reader:      reader,
		contentType: form.FormDataContentType(),
		slow:        true,
	}, out)
	if err != nil {
		// Unblock the writer if the request died before it drained the pipe,
		// otherwise the goroutine leaks holding the open file.
		reader.CloseWithError(err)
	}
	return err
}

// writeUploadBody lays out the multipart body: the extra fields first, so a
// server streaming the request has them before the bytes it needs them for,
// then the file.
func writeUploadBody(form *multipart.Writer, file io.Reader, opts UploadOptions, fields map[string]string) error {
	for name, value := range fields {
		if err := form.WriteField(name, value); err != nil {
			return err
		}
	}

	// Not CreateFormFile: it hardcodes application/octet-stream. Rocket.Chat
	// checks this header against its media-type whitelist and stores it on the
	// attachment, so the default would risk outright rejection and would land
	// every image as an opaque download that no client renders inline.
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`,
		escapeQuotes(opts.Filename)))
	header.Set("Content-Type", opts.MIME)
	part, err := form.CreatePart(header)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, file); err != nil {
		return err
	}
	return form.Close()
}

// escapeQuotes keeps a filename from breaking out of the Content-Disposition
// header it is quoted inside.
func escapeQuotes(name string) string {
	return strings.NewReplacer("\\", "\\\\", `"`, "\\\"", "\r", "", "\n", "").Replace(name)
}

// statUpload checks the path is something we can actually send, and returns the
// name to send it under.
func statUpload(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("rocket: upload requires a file")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("rocket: upload %s: %w", filepath.Base(path), err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("rocket: %s is a directory", filepath.Base(path))
	}
	if !info.Mode().IsRegular() {
		// A fifo or device has no length and may never end; streaming one into
		// a request would hang rather than fail.
		return "", fmt.Errorf("rocket: %s is not a regular file", filepath.Base(path))
	}
	return filepath.Base(path), nil
}

// DetectMIME works out what a file is, extension first and contents second.
//
// The extension is preferred because it is what the uploader named the file, and
// because sniffing is deliberately conservative: http.DetectContentType knows
// nothing about most document formats and answers application/octet-stream for
// them, which is exactly the answer that gets an upload refused.
func DetectMIME(path string) (string, error) {
	if kind := mime.TypeByExtension(filepath.Ext(path)); kind != "" {
		if cut := strings.IndexByte(kind, ';'); cut >= 0 {
			kind = strings.TrimSpace(kind[:cut])
		}
		return kind, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("rocket: read %s: %w", filepath.Base(path), err)
	}
	defer file.Close()

	// DetectContentType looks at no more than this and pads a short read itself.
	head := make([]byte, 512)
	n, err := file.Read(head)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("rocket: read %s: %w", filepath.Base(path), err)
	}
	kind := http.DetectContentType(head[:n])
	if cut := strings.IndexByte(kind, ';'); cut >= 0 {
		kind = strings.TrimSpace(kind[:cut])
	}
	return kind, nil
}

// isMissingRoute reports whether the server answered "no such endpoint", which
// is how a server without rooms.media declines it.
func isMissingRoute(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

func (c *Client) usesLegacyUpload() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.legacyUpload
}

func (c *Client) markLegacyUpload() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.legacyUpload = true
}
