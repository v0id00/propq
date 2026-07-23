// Package config loads and parses the TOML configuration file.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Defaults holds all configurable execution defaults.
// Every field maps to a CLI flag; config values are the base defaults
// that CLI flags can override.
type Defaults struct {
	// Connection / execution
	Timeout     int  `toml:"timeout,omitempty"`
	Concurrency int  `toml:"concurrency,omitempty"`
	NoTxn       bool `toml:"no_transaction,omitempty"`

	// Safety
	Force  bool `toml:"force,omitempty"`
	DryRun bool `toml:"dry_run,omitempty"`

	// Output
	JSON  bool `toml:"json,omitempty"`
	Quiet bool `toml:"quiet,omitempty"`

	// Default filters (applied when CLI flags are not given)
	ServerFilter string `toml:"server,omitempty"`
	DBFilter     string `toml:"dbfilter,omitempty"`
	ExcludeDB    string `toml:"exclude_db,omitempty"`

	// When no --dbfilter / --exclude-db is provided (neither via CLI nor config),
	// prompt for confirmation before hitting ALL databases.
	// Set to false to skip confirmation and run directly.
	ConfirmWithoutFilter bool `toml:"confirm_without_filter,omitempty"`

	// Editor binary to use for --edit and --select modes.
	// Examples: "vim", "nano", "code --wait", "/usr/bin/helix"
	// If empty, uses $VISUAL, then $EDITOR, then vim/nano/hx/micro.
	Editor string `toml:"editor,omitempty"`
}

// SetDefaults fills zero-valued fields with sensible defaults.
// Called after merging with CLI overrides.
func (d *Defaults) SetDefaults() {
	if d.Timeout <= 0 {
		d.Timeout = 30
	}
	if d.Concurrency <= 0 {
		d.Concurrency = 5
	}
}

// Connection defines a single MySQL/MariaDB server.
type Connection struct {
	Name           string   `toml:"-"` // set from map key
	Host           string   `toml:"host"`
	Port           int      `toml:"port,omitempty"`
	User           string   `toml:"user"`
	Password       string   `toml:"password"`
	MaxConnections int      `toml:"max_connections,omitempty"`
	Tags           []string `toml:"tags,omitempty"`
}

// DSN builds a MySQL DSN string.
func (c Connection) DSN(dbName string, timeout int) string {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/", c.User, c.Password, c.Host, c.Port)
	if dbName != "" {
		dsn += dbName
	}
	dsn += fmt.Sprintf("?timeout=%ds&readTimeout=%ds&writeTimeout=%ds", timeout, timeout, timeout)
	dsn += "&multiStatements=true"
	return dsn
}

// Config is the top-level TOML configuration.
type Config struct {
	Defaults    Defaults              `toml:"defaults,omitempty"`
	Connections map[string]Connection `toml:"connections"`
}

// Load reads and parses a TOML config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Ensure connections map
	if cfg.Connections == nil {
		cfg.Connections = make(map[string]Connection)
	}

	// Apply per-connection defaults
	for name, conn := range cfg.Connections {
		conn.Name = name
		if conn.Port == 0 {
			conn.Port = 3306
		}
		if conn.MaxConnections == 0 {
			conn.MaxConnections = 3
		}
		if conn.Tags == nil {
			conn.Tags = []string{}
		}
		cfg.Connections[name] = conn
	}

	// Apply global defaults (only for fields that are still zero)
	cfg.Defaults.SetDefaults()

	return &cfg, nil
}

// FindConfigPath searches for a config file. Priority:
//  1. explicit path (if non-empty)
//  2. ./propq.toml
//  3. Platform-specific config directory:
//     Linux/macOS: ~/.config/propq/config.toml
//     Windows:     %APPDATA%/propq/config.toml
func FindConfigPath(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("config file not found: %s", explicit)
		}
		return explicit, nil
	}

	candidates := []string{"propq.toml"}

	// Platform-specific config directory
	path := PlatformConfigPath()
	if path != "" {
		candidates = append(candidates, path)
	}

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("no config file found. Searched: %s", strings.Join(candidates, ", "))
}

// PlatformConfigPath returns the platform-specific config file path.
func PlatformConfigPath() string {
	if runtime.GOOS == "windows" {
		appData := os.Getenv("APPDATA")
		if appData == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return ""
			}
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(appData, "propq", "config.toml")
	}

	// Linux / macOS: XDG spec
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, ".config", "propq", "config.toml")
	}
	return filepath.Join(xdg, "propq", "config.toml")
}

// DefaultExample returns a default config file content as a string.
func DefaultExample() string {
	return `# propq.toml — v0id00/propq configuration
# See https://github.com/v0id00/propq for full documentation.

[defaults]
timeout = 30
concurrency = 5
confirm_without_filter = true
editor = "vim"

[connections.local]
host = "127.0.0.1"
port = 3306
user = "root"
password = ""
max_connections = 3
tags = ["dev"]
`
}

// ExpandPath expands ~ to the user's home directory.
func ExpandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}
