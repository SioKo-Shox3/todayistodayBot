package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_FromEnv(t *testing.T) {
	t.Setenv("DISCORD_TOKEN", "env-token-123")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.DiscordToken != "env-token-123" {
		t.Fatalf("expected token from env, got %q", cfg.DiscordToken)
	}
}

func TestLoad_FromFileFallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"discord_token":"file-token-456"}`), 0o600); err != nil {
		t.Fatalf("failed to write test config file: %v", err)
	}
	t.Setenv("CONFIG_PATH", path)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.DiscordToken != "file-token-456" {
		t.Fatalf("expected token from file, got %q", cfg.DiscordToken)
	}
}

func TestLoad_FailFastWhenNeitherSet(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CONFIG_PATH", filepath.Join(dir, "does-not-exist.json"))

	_, err := Load()
	if err == nil {
		t.Fatal("expected Load() to return an error when no token is configured, got nil")
	}
}
