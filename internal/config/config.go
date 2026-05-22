package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is the user-facing configuration loaded from a TOML file.
// Secrets and dynamic debug flags are intentionally read from the environment
// (BACKLOG_API_KEY, LOG_LEVEL) and are not part of this struct.
type Config struct {
	Domain          string        `toml:"domain"`
	ListenAddr      string        `toml:"listen_addr"`
	Port            string        `toml:"port"`
	CachePath       string        `toml:"cache_path"`
	ShutdownTimeout time.Duration `toml:"shutdown_timeout"`
	FetchInterval   time.Duration `toml:"fetch_interval"`
	// AllowedLinkPrefixes はコメント本文中の [text](url) を <a href> として有効化する
	// URL prefix の追加 allowlist。https://<Domain>/ は自動で許可されるため、ここには
	// それ以外で許可したい外部 URL を列挙する。空の場合は外部 URL は無効化 (#) される。
	AllowedLinkPrefixes []string `toml:"allowed_link_prefixes"`
}

const (
	defaultListenAddr      = "127.0.0.1"
	defaultPort            = "8082"
	defaultShutdownTimeout = 10 * time.Second
	defaultFetchInterval   = 15 * time.Minute
)

// Load reads the config TOML from path (or the default location when empty),
// applies defaults, and validates required fields.
func Load(path string) (Config, error) {
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return Config{}, err
		}
	}

	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		if os.IsNotExist(err) {
			return Config{}, fmt.Errorf("config file not found at %s (see config.example.toml)", path)
		}
		return Config{}, fmt.Errorf("decode %s: %w", path, err)
	}

	if err := cfg.applyDefaults(); err != nil {
		return Config{}, err
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// DefaultPath returns ~/.config/backlog-board/config.toml, honoring XDG_CONFIG_HOME.
func DefaultPath() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "backlog-board", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "backlog-board", "config.toml"), nil
}

func (c *Config) applyDefaults() error {
	if c.ListenAddr == "" {
		c.ListenAddr = defaultListenAddr
	}
	if c.Port == "" {
		c.Port = defaultPort
	}
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = defaultShutdownTimeout
	}
	if c.FetchInterval == 0 {
		c.FetchInterval = defaultFetchInterval
	}
	if c.CachePath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home dir: %w", err)
		}
		c.CachePath = filepath.Join(home, ".local", "share", "backlog-board", "snapshot.json")
	}
	return nil
}

func (c *Config) validate() error {
	if c.Domain == "" {
		return fmt.Errorf("domain is required (set 'domain = \"yourspace.backlog.com\"' in config.toml)")
	}
	return nil
}
