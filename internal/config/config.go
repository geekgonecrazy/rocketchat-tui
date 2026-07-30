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

	path string `json:"-"`
}

// Paths resolves the config file and cache database locations, honouring
// XDG_CONFIG_HOME / XDG_DATA_HOME.
func Paths() (configPath, dbPath string, err error) {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	dataHome := os.Getenv("XDG_DATA_HOME")
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
	return filepath.Join(configHome, "rctui", "config.json"),
		filepath.Join(dataHome, "rctui", "cache.db"),
		nil
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
