package rocket_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/geekgonecrazy/rocketchat-tui/internal/fakerc"
	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
)

// writeTempFile drops content on disk under name and returns the path.
func writeTempFile(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// uploadClient is a client pointed at a fake server and holding its credentials.
func uploadClient(t *testing.T, server *fakerc.Server) *rocket.Client {
	t.Helper()
	client, err := rocket.NewClient(server.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client.SetCredentials(rocket.Credentials{
		ServerURL: server.URL,
		UserID:    fakerc.UserID,
		AuthToken: fakerc.AuthToken,
	})
	return client
}

func TestUploadUsesMediaRouteAndCarriesTheMessage(t *testing.T) {
	server := fakerc.New(t)
	client := uploadClient(t, server)
	path := writeTempFile(t, "shot.png", fakerc.FilePNG)

	message, err := client.Upload(context.Background(), rocket.UploadOptions{
		RoomID: "room-1",
		Path:   path,
		Text:   "here it is",
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if message.ID == "" {
		t.Error("expected the confirmed message back")
	}

	uploads := server.Uploads()
	if len(uploads) != 1 {
		t.Fatalf("got %d uploads, want 1", len(uploads))
	}
	got := uploads[0]
	if got.Route != "media" {
		t.Errorf("route = %q, want the modern media route", got.Route)
	}
	if got.RoomID != "room-1" || got.Filename != "shot.png" {
		t.Errorf("upload = %+v, want room-1/shot.png", got)
	}
	if got.Text != "here it is" {
		t.Errorf("text = %q, want it carried on the confirm call", got.Text)
	}
	if string(got.Bytes) != string(fakerc.FilePNG) {
		t.Errorf("uploaded %d bytes, want the %d the file holds", len(got.Bytes), len(fakerc.FilePNG))
	}
}

// The part's declared type is what a server stores and whitelists against, so
// an image that arrives as application/octet-stream is an image no client will
// ever draw. Go's CreateFormFile would do exactly that.
func TestUploadDeclaresTheFilesMediaType(t *testing.T) {
	cases := []struct {
		name    string
		content []byte
		want    string
	}{
		{"shot.png", fakerc.FilePNG, "image/png"},
		{"notes.txt", []byte("plain words"), "text/plain"},
		// No extension to go on, so the type has to come from the bytes.
		{"screenshot", fakerc.FilePNG, "image/png"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := fakerc.New(t)
			client := uploadClient(t, server)

			_, err := client.Upload(context.Background(), rocket.UploadOptions{
				RoomID: "room-1",
				Path:   writeTempFile(t, tc.name, tc.content),
			})
			if err != nil {
				t.Fatalf("Upload: %v", err)
			}

			uploads := server.Uploads()
			if len(uploads) != 1 {
				t.Fatalf("got %d uploads, want 1", len(uploads))
			}
			// text/plain arrives with a charset; the family is what matters.
			if kind, _, _ := strings.Cut(uploads[0].MIME, ";"); kind != tc.want {
				t.Errorf("declared type = %q, want %q", uploads[0].MIME, tc.want)
			}
		})
	}
}

func TestUploadFallsBackToLegacyRouteOnceAndThenStaysThere(t *testing.T) {
	server := fakerc.New(t)
	server.NoMediaRoute = true
	client := uploadClient(t, server)

	for _, name := range []string{"one.png", "two.png"} {
		_, err := client.Upload(context.Background(), rocket.UploadOptions{
			RoomID:   "room-1",
			Path:     writeTempFile(t, name, fakerc.FilePNG),
			Text:     "with text",
			ThreadID: "thread-9",
		})
		if err != nil {
			t.Fatalf("Upload %s: %v", name, err)
		}
	}

	uploads := server.Uploads()
	if len(uploads) != 2 {
		t.Fatalf("got %d uploads, want 2", len(uploads))
	}
	for _, got := range uploads {
		if got.Route != "upload" {
			t.Errorf("%s went via %q, want the legacy route", got.Filename, got.Route)
		}
		// The legacy route carries the message as form fields beside the file
		// rather than in a second call, so both still have to arrive.
		if got.Text != "with text" || got.ThreadID != "thread-9" {
			t.Errorf("%s lost its message: text=%q tmid=%q", got.Filename, got.Text, got.ThreadID)
		}
	}

	// Re-enabling the route proves the client stopped asking: a client that
	// retried media every time would take it again here.
	server.NoMediaRoute = false
	if _, err := client.Upload(context.Background(), rocket.UploadOptions{
		RoomID: "room-1",
		Path:   writeTempFile(t, "three.png", fakerc.FilePNG),
	}); err != nil {
		t.Fatalf("Upload three.png: %v", err)
	}
	if got := server.Uploads()[2].Route; got != "upload" {
		t.Errorf("route = %q, want the client to have remembered the fallback", got)
	}
}

func TestUploadReportsServerRefusal(t *testing.T) {
	server := fakerc.New(t)
	server.RejectUpload = true
	client := uploadClient(t, server)

	_, err := client.Upload(context.Background(), rocket.UploadOptions{
		RoomID: "room-1",
		Path:   writeTempFile(t, "shot.png", fakerc.FilePNG),
	})
	if err == nil {
		t.Fatal("expected an error when the server refuses the file")
	}
	// A refusal is not a missing route, so it must not silently downgrade the
	// client to the legacy path for the rest of the session.
	if !strings.Contains(err.Error(), "invalid-file-type") {
		t.Errorf("error should carry the server's reason, got %v", err)
	}
}

func TestUploadRefusesWhatCannotBeSent(t *testing.T) {
	server := fakerc.New(t)
	client := uploadClient(t, server)
	dir := t.TempDir()

	cases := []struct {
		name string
		opts rocket.UploadOptions
		want string
	}{
		{"no room", rocket.UploadOptions{Path: writeTempFile(t, "a.png", fakerc.FilePNG)}, "room id"},
		{"no path", rocket.UploadOptions{RoomID: "room-1"}, "requires a file"},
		{"missing file", rocket.UploadOptions{RoomID: "room-1", Path: filepath.Join(dir, "nope.png")}, "nope.png"},
		{"a directory", rocket.UploadOptions{RoomID: "room-1", Path: dir}, "directory"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := client.Upload(context.Background(), tc.opts); err == nil {
				t.Fatal("expected a refusal")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
	if len(server.Uploads()) != 0 {
		t.Error("nothing should have reached the server")
	}
}
