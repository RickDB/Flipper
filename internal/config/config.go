// Package config loads Flipper's runtime configuration from environment
// variables, all prefixed FLIPPER_. Everything has a sane default so the
// binary runs with zero configuration; Docker Compose sets the couple of
// variables worth overriding.
package config

import (
	"os"
	"strings"
)

type Config struct {
	// ListenAddress is the address net/http listens on, e.g. ":19012".
	ListenAddress string

	// DatabasePath is the path to the SQLite database file.
	DatabasePath string

	// InitialAdminUsername/InitialAdminPassword, when both set and no admin
	// account exists yet, bootstrap the admin account non-interactively on
	// startup — handy for Docker Compose. When unset, Flipper falls back to
	// a one-time /setup page on first run.
	InitialAdminUsername string
	InitialAdminPassword string
}

func Load() Config {
	c := Config{
		ListenAddress: ":19012",
		DatabasePath:  "data/flipper.db",
	}
	if v := strings.TrimSpace(os.Getenv("FLIPPER_LISTEN")); v != "" {
		c.ListenAddress = v
	}
	if v := strings.TrimSpace(os.Getenv("FLIPPER_DB")); v != "" {
		c.DatabasePath = v
	}
	c.InitialAdminUsername = strings.TrimSpace(os.Getenv("FLIPPER_INITIAL_USERNAME"))
	c.InitialAdminPassword = os.Getenv("FLIPPER_INITIAL_PASSWORD")
	return c
}
