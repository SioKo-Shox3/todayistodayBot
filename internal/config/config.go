package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config holds the runtime configuration for the bot. Phase A only needs
// the Discord bot token — the .NET version's Bot:FrameRate setting drove a
// hand-rolled FPS main loop that this Go rewrite removes entirely (see
// Docs/superpowers/specs/2026-07-05-go-migration-phase-a-design.md).
type Config struct {
	DiscordToken string `json:"discord_token"`
}

const defaultConfigPath = "./config.json"

// Load resolves configuration with environment variables taking priority
// over a fallback config file, and fails fast (non-nil error) if neither
// source provides a Discord token.
//
// Resolution order:
//  1. DISCORD_TOKEN environment variable.
//  2. JSON file at CONFIG_PATH (default "./config.json") with a
//     "discord_token" field.
//  3. Neither present => error.
func Load() (*Config, error) {
	if token := os.Getenv("DISCORD_TOKEN"); token != "" {
		return &Config{DiscordToken: token}, nil
	}

	path := os.Getenv("CONFIG_PATH")
	if path == "" {
		path = defaultConfigPath
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: DISCORD_TOKEN not set and config file %q not readable: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: failed to parse %q: %w", path, err)
	}

	if cfg.DiscordToken == "" {
		return nil, fmt.Errorf("config: %q did not contain a non-empty discord_token", path)
	}

	return &cfg, nil
}
