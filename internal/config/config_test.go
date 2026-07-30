package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/geekgonecrazy/rocketchat-tui/internal/config"
)

func TestLoadMissingFileReturnsEmptyConfig(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HasSession() {
		t.Error("a missing config should not report a session")
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.ServerURL = "https://chat.example.com"
	cfg.UserID = "user-1"
	cfg.AuthToken = "secret-token"
	cfg.Username = "tester"
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.HasSession() {
		t.Fatal("expected a session after saving credentials")
	}
	if reloaded.AuthToken != "secret-token" || reloaded.UserID != "user-1" {
		t.Errorf("credentials did not round-trip: %+v", reloaded)
	}
}

func TestSaveUsesOwnerOnlyPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.ServerURL = "https://chat.example.com"
	cfg.AuthToken = "secret-token"
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The file holds an auth token, so it must not be world-readable.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions = %o, want 600", perm)
	}
}

func TestSaveLeavesNoTempFileBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.ServerURL = "https://chat.example.com"
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Errorf("atomic write left %s behind", entry.Name())
		}
	}
}

func TestClearSessionKeepsServerURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.ServerURL = "https://chat.example.com"
	cfg.UserID = "user-1"
	cfg.AuthToken = "secret-token"
	cfg.Username = "tester"

	cfg.ClearSession()

	if cfg.HasSession() {
		t.Error("session should be gone")
	}
	if cfg.ServerURL != "https://chat.example.com" {
		t.Errorf("server URL = %q, want it kept so the login form can prefill it", cfg.ServerURL)
	}
}

func TestLoadRejectsCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := config.Load(path); err == nil {
		t.Error("expected an error for a corrupt config file")
	}
}

func TestPathsHonourXDGVariables(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-config")
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-data")

	configPath, dbPath, err := config.Paths()
	if err != nil {
		t.Fatalf("Paths: %v", err)
	}
	if configPath != "/tmp/xdg-config/rctui/config.json" {
		t.Errorf("config path = %q", configPath)
	}
	if dbPath != "/tmp/xdg-data/rctui/cache.db" {
		t.Errorf("db path = %q", dbPath)
	}
}

func TestDownloadsPrefersTheConfiguredDirectory(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{DownloadDir: dir}
	if got := cfg.Downloads(); got != dir {
		t.Errorf("Downloads() = %q, want the configured %q", got, dir)
	}
}

func TestDownloadsExpandsTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory in this environment")
	}
	cfg := &config.Config{DownloadDir: filepath.Join("~", "elsewhere")}
	if want := filepath.Join(home, "elsewhere"); cfg.Downloads() != want {
		t.Errorf("Downloads() = %q, want %q", cfg.Downloads(), want)
	}
}

func TestDownloadsFallsBackWhenUnset(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory in this environment")
	}

	got := (&config.Config{}).Downloads()
	// Either ~/Downloads when the platform made one, or the home directory —
	// but always somewhere that exists and belongs to the user.
	if got != filepath.Join(home, "Downloads") && got != home {
		t.Errorf("Downloads() = %q, want ~/Downloads or the home directory", got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Errorf("default download directory %q is not usable: %v", got, err)
	}
}
