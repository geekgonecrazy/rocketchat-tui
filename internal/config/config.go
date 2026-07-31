// Package config persists the server URL and auth token between runs so launch
// does not require logging in again.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Config is the on-disk state. The auth token is a credential, so the file is
// written 0600 and never logged.
type Config struct {
	ServerURL string `json:"server_url"`
	UserID    string `json:"user_id"`
	AuthToken string `json:"auth_token"`
	Username  string `json:"username"`

	// Theme selects the colour palette; "" means the default.
	Theme string `json:"theme,omitempty"`

	// DownloadDir is where saved attachments are written; "" means the default,
	// resolved by Downloads.
	DownloadDir string `json:"download_dir,omitempty"`

	// Editor is the command used to compose long messages; "" falls back to
	// $VISUAL, then $EDITOR.
	Editor string `json:"editor,omitempty"`

	// Notifications controls what happens when someone addresses you.
	Notifications Notifications `json:"notifications"`

	path string `json:"-"`
}

// Notifications is the user's answer to "tell me when someone needs me".
//
// The two switches are pointers so that absent and false are different things.
// Both default to on, and a config file written before this existed — or by
// hand, without them — has to mean "on" rather than "the user turned these off",
// which is what a plain bool would silently have said.
type Notifications struct {
	// Desktop raises a system notification. Defaults to on.
	Desktop *bool `json:"desktop,omitempty"`
	// Sound rings the terminal bell, or runs SoundCommand. Defaults to on.
	Sound *bool `json:"sound,omitempty"`

	// SoundCommand replaces the bell with a shell command, e.g.
	// "paplay ~/sounds/ping.wav". Empty means the bell.
	SoundCommand string `json:"sound_command,omitempty"`
}

// DesktopEnabled reports whether desktop notifications are on.
func (n Notifications) DesktopEnabled() bool { return n.Desktop == nil || *n.Desktop }

// SoundEnabled reports whether notifications make a sound.
func (n Notifications) SoundEnabled() bool { return n.Sound == nil || *n.Sound }

// SetDesktop turns desktop notifications on or off.
func (n *Notifications) SetDesktop(on bool) { n.Desktop = &on }

// SetSound turns the notification sound on or off.
func (n *Notifications) SetSound(on bool) { n.Sound = &on }

// Downloads is where saved attachments go: whatever the user configured, else
// ~/Downloads if the platform made one, else the home directory. It is only a
// preferred location — nothing is created until something is actually saved.
func (c *Config) Downloads() string {
	if configured := strings.TrimSpace(c.DownloadDir); configured != "" {
		if expanded, err := expandHome(configured); err == nil {
			return expanded
		}
		return configured
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	downloads := filepath.Join(home, "Downloads")
	if info, err := os.Stat(downloads); err == nil && info.IsDir() {
		return downloads
	}
	return home
}

// EditorCommand is the command that composes a long message: whatever the user
// configured, else $VISUAL, else $EDITOR, else "" — there is deliberately no
// hard-coded vi fallback, because trapping a non-vi user in a modal editor from
// a chat client is worse than saying nothing happened and why.
//
// Pass os.Getenv; the indirection is so the precedence can be tested without
// mutating the process environment.
func (c *Config) EditorCommand(getenv func(string) string) string {
	if configured := strings.TrimSpace(c.Editor); configured != "" {
		return configured
	}
	if visual := strings.TrimSpace(getenv("VISUAL")); visual != "" {
		return visual
	}
	return strings.TrimSpace(getenv("EDITOR"))
}

// expandHome resolves a leading ~, which people write in config files and
// which nothing in the standard library expands for us.
func expandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~"+string(os.PathSeparator)) {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~")), nil
}

// Paths resolves the config file and cache database locations, honouring
// XDG_CONFIG_HOME / XDG_DATA_HOME.
func Paths() (configPath, dbPath string, err error) {
	configHome, dataHome, err := xdgHomes()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(configHome, "rctui", "config.json"),
		filepath.Join(dataHome, "rctui", "cache.db"),
		nil
}

// ComposePath is the file handed to the user's editor when they compose a long
// message. It sits beside the cache rather than in a temp directory on purpose:
// it is the recovery copy, so it must still be there tomorrow.
func ComposePath() (string, error) {
	_, dataHome, err := xdgHomes()
	if err != nil {
		return "", err
	}
	return filepath.Join(dataHome, "rctui", "compose.md"), nil
}

// xdgHomes resolves the config and data roots, falling back to the conventional
// locations under the home directory.
func xdgHomes() (configHome, dataHome string, err error) {
	configHome = os.Getenv("XDG_CONFIG_HOME")
	dataHome = os.Getenv("XDG_DATA_HOME")
	if configHome == "" || dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", fmt.Errorf("config: locate home directory: %w", err)
		}
		if configHome == "" {
			configHome = filepath.Join(home, ".config")
		}
		if dataHome == "" {
			dataHome = filepath.Join(home, ".local", "share")
		}
	}
	return configHome, dataHome, nil
}

// Load reads the config, returning an empty one if the file does not exist.
func Load(path string) (*Config, error) {
	cfg := &Config{path: path}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cfg, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	cfg.path = path
	cfg.ServerURL = strings.TrimSpace(cfg.ServerURL)
	return cfg, nil
}

// Save writes the config atomically with owner-only permissions.
func (c *Config) Save() error {
	if c.path == "" {
		return errors.New("config: no path set")
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return fmt.Errorf("config: create config dir: %w", err)
	}
	encoded, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("config: encode: %w", err)
	}

	// Write to a temp file in the same directory, then rename, so an interrupted
	// write can never leave a truncated config behind.
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("config: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, c.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("config: replace %s: %w", c.path, err)
	}
	return nil
}

// Path is where this config was loaded from, and where Save writes.
func (c *Config) Path() string { return c.path }

// HasSession reports whether cached credentials are present.
func (c *Config) HasSession() bool {
	return c.ServerURL != "" && c.UserID != "" && c.AuthToken != ""
}

// ClearSession drops the cached credentials, keeping the server URL so the
// login screen can prefill it.
func (c *Config) ClearSession() {
	c.UserID = ""
	c.AuthToken = ""
	c.Username = ""
}
