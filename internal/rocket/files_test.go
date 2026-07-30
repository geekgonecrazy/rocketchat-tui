package rocket_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
)

// fileServer records what a download request looked like and answers with body.
type fileServer struct {
	*httptest.Server
	path      string
	authToken string
	userID    string
}

func newFileServer(t *testing.T, body []byte, contentType string) *fileServer {
	t.Helper()
	recorder := &fileServer{}
	recorder.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.path = r.URL.Path
		recorder.authToken = r.Header.Get("X-Auth-Token")
		recorder.userID = r.Header.Get("X-User-Id")
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.Write(body)
	}))
	t.Cleanup(recorder.Close)
	return recorder
}

func authedClient(t *testing.T, serverURL string) *rocket.Client {
	t.Helper()
	client, err := rocket.NewClient(serverURL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client.SetCredentials(rocket.Credentials{
		ServerURL: serverURL,
		UserID:    "user-1",
		AuthToken: "secret-token",
	})
	return client
}

func TestDownloadResolvesRelativeRefAndSendsCredentials(t *testing.T) {
	server := newFileServer(t, []byte("png bytes"), "image/png; charset=binary")
	client := authedClient(t, server.URL)

	file, err := client.Download(context.Background(), "/file-upload/abc123/shot.png")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}

	if got := string(file.Data); got != "png bytes" {
		t.Errorf("body = %q, want %q", got, "png bytes")
	}
	// The charset parameter is noise for our purposes and must be stripped, or
	// every "is this an image" comparison has to know about it.
	if file.MIME != "image/png" {
		t.Errorf("MIME = %q, want %q", file.MIME, "image/png")
	}
	if server.path != "/file-upload/abc123/shot.png" {
		t.Errorf("requested %q, want the ref resolved against the server URL", server.path)
	}
	if server.authToken != "secret-token" || server.userID != "user-1" {
		t.Errorf("credentials were not sent: token=%q user=%q", server.authToken, server.userID)
	}
}

func TestDownloadWithholdsCredentialsFromOtherHosts(t *testing.T) {
	// An attachment URL is server-supplied and can name any host at all, so an
	// absolute URL elsewhere must not be handed our session token.
	elsewhere := newFileServer(t, []byte("someone else's bytes"), "image/jpeg")
	client := authedClient(t, "https://chat.example.com")

	file, err := client.Download(context.Background(), elsewhere.URL+"/preview.jpg")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(file.Data) != "someone else's bytes" {
		t.Errorf("unexpected body %q", file.Data)
	}
	if elsewhere.authToken != "" || elsewhere.userID != "" {
		t.Errorf("leaked credentials to a third-party host: token=%q user=%q",
			elsewhere.authToken, elsewhere.userID)
	}
}

func TestDownloadRequiresAuthenticationForOwnHost(t *testing.T) {
	server := newFileServer(t, []byte("x"), "image/png")
	client, err := rocket.NewClient(server.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	if _, err := client.Download(context.Background(), "/file-upload/a/b.png"); err == nil {
		t.Fatal("expected an error when downloading without credentials")
	} else if !strings.Contains(err.Error(), "authentication") {
		t.Errorf("error should mention authentication, got %v", err)
	}
}

func TestDownloadRejectsOversizedBody(t *testing.T) {
	server := newFileServer(t, make([]byte, rocket.MaxDownloadBytes+1), "image/png")
	client := authedClient(t, server.URL)

	_, err := client.Download(context.Background(), "/file-upload/huge.png")
	if err == nil {
		t.Fatal("expected an error for a body over the cap")
	}
	if !strings.Contains(err.Error(), "larger than") {
		t.Errorf("error should say the file is too large, got %v", err)
	}
}

func TestDownloadReportsServerErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer server.Close()

	client := authedClient(t, server.URL)
	if _, err := client.Download(context.Background(), "/file-upload/missing.png"); err == nil {
		t.Fatal("expected an error for a 404")
	}
}

func TestDownloadRejectsUnsupportedSchemes(t *testing.T) {
	client := authedClient(t, "https://chat.example.com")

	for _, ref := range []string{"", "   ", "file:///etc/passwd", "javascript:alert(1)"} {
		if _, err := client.Download(context.Background(), ref); err == nil {
			t.Errorf("Download(%q) should have been refused", ref)
		}
	}
}
