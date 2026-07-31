//go:build integration

// Integration tests that run against a real Rocket.Chat server started with
// testcontainers. They need a working Docker endpoint, so they are behind a
// build tag and excluded from the default suite:
//
//	cd test/integration && go test -tags integration -timeout 20m -v
//
// The fake server in internal/fakerc covers the same ground without Docker and
// is what CI runs; this suite exists to catch places where the real server's
// wire format has drifted from what the fake reproduces.
package integration

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcmongo "github.com/testcontainers/testcontainers-go/modules/mongodb"
	tcnetwork "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/geekgonecrazy/rocketchat-tui/internal/app"
	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
	"github.com/geekgonecrazy/rocketchat-tui/internal/store"
)

const (
	rocketChatImage = "registry.rocket.chat/rocketchat/rocket.chat:8.0.1"
	mongoImage      = "mongo:8.0"

	adminUser = "admin"
	adminPass = "integration-pass-123"
)

// liveServer is a running Rocket.Chat plus its base URL.
type liveServer struct {
	baseURL string
}

// requireDocker skips rather than fails when there is no container runtime, so
// running this suite on a machine without Docker reports "skip", not a red test.
// testcontainers honours DOCKER_HOST, so a remote daemon or Testcontainers Cloud
// endpoint works here too.
func requireDocker(t *testing.T) {
	t.Helper()
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		t.Skipf("no Docker endpoint available (set DOCKER_HOST to use a remote one): %v", err)
	}
	defer provider.Close()
	if err := provider.Health(context.Background()); err != nil {
		t.Skipf("Docker endpoint is not healthy: %v", err)
	}
}

// startRocketChat boots MongoDB (as a replica set, which Rocket.Chat requires)
// and Rocket.Chat on a shared network, seeded with an admin account.
func startRocketChat(t *testing.T) liveServer {
	t.Helper()
	requireDocker(t)
	ctx := context.Background()

	net, err := tcnetwork.New(ctx)
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	testcontainers.CleanupNetwork(t, net)

	mongo, err := tcmongo.Run(ctx, mongoImage,
		tcmongo.WithReplicaSet("rs0"),
		tcnetwork.WithNetwork([]string{"mongodb"}, net),
	)
	testcontainers.CleanupContainer(t, mongo)
	if err != nil {
		t.Fatalf("start mongodb: %v", err)
	}

	// Rocket.Chat reaches Mongo by its network alias, not the mapped host port.
	mongoURL := "mongodb://mongodb:27017/rocketchat?replicaSet=rs0"
	oplogURL := "mongodb://mongodb:27017/local?replicaSet=rs0"

	rc, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        rocketChatImage,
			ExposedPorts: []string{"3000/tcp"},
			Networks:     []string{net.Name},
			Env: map[string]string{
				"ROOT_URL":                            "http://localhost:3000",
				"PORT":                                "3000",
				"MONGO_URL":                           mongoURL,
				"MONGO_OPLOG_URL":                     oplogURL,
				"DEPLOY_METHOD":                       "docker",
				"ADMIN_USERNAME":                      adminUser,
				"ADMIN_PASS":                          adminPass,
				"ADMIN_EMAIL":                         "admin@example.com",
				"OVERWRITE_SETTING_Show_Setup_Wizard": "completed",
			},
			// First boot builds indexes and can take a while on cold caches.
			WaitingFor: wait.ForHTTP("/api/info").
				WithPort("3000/tcp").
				WithStartupTimeout(8 * time.Minute),
		},
		Started: true,
	})
	testcontainers.CleanupContainer(t, rc)
	if err != nil {
		t.Fatalf("start rocket.chat: %v", err)
	}

	host, err := rc.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	port, err := rc.MappedPort(ctx, "3000/tcp")
	if err != nil {
		t.Fatalf("mapped port: %v", err)
	}

	return liveServer{baseURL: fmt.Sprintf("http://%s:%s", host, port.Port())}
}

// harness is a core wired to the live server, recording published events.
type harness struct {
	core  *app.Core
	cache *store.Store

	mu     sync.Mutex
	events []app.Event
}

func newHarness(t *testing.T, server liveServer) *harness {
	t.Helper()

	session, err := app.Login(context.Background(), app.LoginParams{
		ServerURL: server.baseURL,
		Username:  adminUser,
		Password:  adminPass,
	})
	if err != nil {
		t.Fatalf("login against the live server: %v", err)
	}

	cache, err := store.Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = cache.Close() })

	core := app.New(session.Client, cache, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go core.Run(ctx)

	h := &harness{core: core, cache: cache}
	go func() {
		for event := range core.Events() {
			h.mu.Lock()
			h.events = append(h.events, event)
			h.mu.Unlock()
		}
	}()
	core.Start(session.UserID, session.Username)
	return h
}

func (h *harness) snapshot() []app.Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]app.Event(nil), h.events...)
}

// TestLiveServerEndToEnd exercises the paths most likely to drift from the fake:
// login, subscription sync, DDP connect, history, send, and realtime echo.
func TestLiveServerEndToEnd(t *testing.T) {
	server := startRocketChat(t)
	h := newHarness(t, server)

	// A fresh Rocket.Chat seeds the admin into #general.
	rooms := waitFor(t, "the sidebar to populate", func() (app.RoomsUpdated, bool) {
		events := h.snapshot()
		for i := len(events) - 1; i >= 0; i-- {
			if snapshot, ok := events[i].(app.RoomsUpdated); ok && len(snapshot.Rooms) > 0 {
				return snapshot, true
			}
		}
		return app.RoomsUpdated{}, false
	})

	var general model.Room
	for _, room := range rooms.Rooms {
		if room.Name == "general" {
			general = room
		}
	}
	if general.ID == "" {
		t.Fatalf("no #general room in %d synced rooms", len(rooms.Rooms))
	}
	if general.Kind != model.KindChannel {
		t.Errorf("#general kind = %v, want channel", general.Kind)
	}

	// The realtime connection must come up against the real DDP endpoint.
	waitFor(t, "realtime to connect", func() (bool, bool) {
		for _, event := range h.snapshot() {
			if status, ok := event.(app.StatusChanged); ok && status.Connection == rocket.Connected {
				return true, true
			}
		}
		return false, false
	})

	h.core.OpenRoom(general.ID)
	waitFor(t, "the timeline to load", func() (bool, bool) {
		for _, event := range h.snapshot() {
			if timeline, ok := event.(app.TimelineUpdated); ok && timeline.RoomID == general.ID {
				return true, true
			}
		}
		return false, false
	})

	// Send, then confirm it comes back through the store as our own message.
	const text = "hello from the rctui integration test"
	h.core.Send(app.SendRequest{RoomID: general.ID, Text: text})

	timeline := waitFor(t, "the sent message to appear", func() (app.TimelineUpdated, bool) {
		events := h.snapshot()
		for i := len(events) - 1; i >= 0; i-- {
			snapshot, ok := events[i].(app.TimelineUpdated)
			if !ok || snapshot.RoomID != general.ID {
				continue
			}
			for _, msg := range snapshot.Messages {
				if msg.Text == text {
					return snapshot, true
				}
			}
		}
		return app.TimelineUpdated{}, false
	})

	for _, msg := range timeline.Messages {
		if msg.Text != text {
			continue
		}
		if !msg.Own {
			t.Error("our own message should be flagged as own")
		}
		if msg.Username != adminUser {
			t.Errorf("author = %q, want %q", msg.Username, adminUser)
		}
		if msg.At.IsZero() {
			t.Error("message timestamp did not decode")
		}
	}

	// Threads: replying with a tmid must produce a thread parent on the server.
	var parentID string
	for _, msg := range timeline.Messages {
		if msg.Text == text {
			parentID = msg.ID
		}
	}
	h.core.Send(app.SendRequest{
		RoomID:   general.ID,
		ThreadID: parentID,
		Text:     "threaded reply from the integration test",
	})
	h.core.OpenThread(general.ID, parentID)

	thread := waitFor(t, "the thread to load", func() (app.ThreadUpdated, bool) {
		events := h.snapshot()
		for i := len(events) - 1; i >= 0; i-- {
			if update, ok := events[i].(app.ThreadUpdated); ok && len(update.Replies) > 0 {
				return update, true
			}
		}
		return app.ThreadUpdated{}, false
	})
	if thread.Parent.ID != parentID {
		t.Errorf("thread parent = %q, want %q", thread.Parent.ID, parentID)
	}
}

// TestLiveTypingIndicator checks the typing notification the real server
// actually accepts, which is the piece most sensitive to version drift.
func TestLiveTypingIndicator(t *testing.T) {
	server := startRocketChat(t)
	h := newHarness(t, server)

	rooms := waitFor(t, "the sidebar to populate", func() (app.RoomsUpdated, bool) {
		events := h.snapshot()
		for i := len(events) - 1; i >= 0; i-- {
			if snapshot, ok := events[i].(app.RoomsUpdated); ok && len(snapshot.Rooms) > 0 {
				return snapshot, true
			}
		}
		return app.RoomsUpdated{}, false
	})

	var roomID string
	for _, room := range rooms.Rooms {
		if room.Name == "general" {
			roomID = room.ID
		}
	}
	if roomID == "" {
		t.Fatal("no #general room found")
	}

	waitFor(t, "realtime to connect", func() (bool, bool) {
		for _, event := range h.snapshot() {
			if status, ok := event.(app.StatusChanged); ok && status.Connection == rocket.Connected {
				return true, true
			}
		}
		return false, false
	})

	h.core.OpenRoom(roomID)
	waitFor(t, "the timeline to load", func() (bool, bool) {
		for _, event := range h.snapshot() {
			if timeline, ok := event.(app.TimelineUpdated); ok && timeline.RoomID == roomID {
				return true, true
			}
		}
		return false, false
	})

	// The server must accept the notification without erroring the connection.
	h.core.UserTyping(roomID)
	time.Sleep(2 * time.Second)
	h.core.StopTyping(roomID)

	for _, event := range h.snapshot() {
		if status, ok := event.(app.StatusChanged); ok && status.Err != nil {
			t.Errorf("typing notification broke the connection: %v", status.Err)
		}
		if notice, ok := event.(app.Notice); ok && notice.IsErr {
			t.Errorf("unexpected error notice: %s", notice.Text)
		}
	}
}
