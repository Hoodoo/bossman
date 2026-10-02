// Package config resolves bossman's data directory and the agent source
// directories it reads from.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is the effective configuration: defaults, overridden by
// <home>/config.toml, overridden by environment variables.
type Config struct {
	// Home holds the archive, the index database, and optional overrides.
	Home string `toml:"-"`
	// ClaudeDir is Claude Code's projects directory.
	ClaudeDir string `toml:"claude_dir"`
	// CodexDir is Codex's home directory (sessions/ and session_index.jsonl).
	CodexDir string `toml:"codex_dir"`
	// IdleMinutes caps the gap between two events that still counts as
	// active time.
	IdleMinutes float64 `toml:"idle_minutes"`
}

// Load builds the configuration. home may be empty to use the default.
func Load(home string) (*Config, error) {
	if home == "" {
		home = os.Getenv("BOSSMAN_HOME")
	}
	if home == "" {
		home = defaultHome()
	}
	uh, _ := os.UserHomeDir()
	c := &Config{
		Home:        home,
		ClaudeDir:   filepath.Join(uh, ".claude", "projects"),
		CodexDir:    filepath.Join(uh, ".codex"),
		IdleMinutes: 5,
	}
	if _, err := toml.DecodeFile(c.ConfigPath(), c); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", c.ConfigPath(), err)
	}
	if v := os.Getenv("BOSSMAN_CLAUDE_DIR"); v != "" {
		c.ClaudeDir = v
	}
	if v := os.Getenv("BOSSMAN_CODEX_DIR"); v != "" {
		c.CodexDir = v
	}
	c.ClaudeDir = expand(c.ClaudeDir)
	c.CodexDir = expand(c.CodexDir)
	if c.IdleMinutes <= 0 {
		c.IdleMinutes = 5
	}
	return c, nil
}

func defaultHome() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "bossman")
	}
	uh, _ := os.UserHomeDir()
	return filepath.Join(uh, ".local", "share", "bossman")
}

func expand(p string) string {
	if len(p) >= 2 && p[:2] == "~/" {
		uh, _ := os.UserHomeDir()
		return filepath.Join(uh, p[2:])
	}
	return p
}

func (c *Config) ConfigPath() string  { return filepath.Join(c.Home, "config.toml") }
func (c *Config) DBPath() string      { return filepath.Join(c.Home, "bossman.db") }
func (c *Config) PricingPath() string { return filepath.Join(c.Home, "pricing.toml") }
func (c *Config) ArchiveDir() string  { return filepath.Join(c.Home, "archive") }

// Idle is the active-time gap cap.
func (c *Config) Idle() time.Duration {
	return time.Duration(c.IdleMinutes * float64(time.Minute))
}
