// Package config reads the server configuration from the environment.
// Every variable is prefixed TM_ and documented in .env.example.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

type Config struct {
	DatabaseURL    string
	ListenAddr     string
	IngestKey      string
	ProjectSalt    string
	CORSOrigins    []string
	DemoMode       bool
	RedactPayloads bool
	TrustedTools   map[string]bool
	LogLevel       slog.Level
	ReplayModel    string
	OpenAIKey      string
}

// Load reads the configuration. It returns an error when a variable that the
// server cannot run without is missing.
func Load() (Config, error) {
	c := Config{
		DatabaseURL:    env("TM_DATABASE_URL", ""),
		ListenAddr:     env("TM_LISTEN_ADDR", ":8080"),
		IngestKey:      env("TM_INGEST_KEY", ""),
		ProjectSalt:    env("TM_PROJECT_SALT", ""),
		CORSOrigins:    splitList(env("TM_CORS_ORIGIN", "http://localhost:5173")),
		DemoMode:       env("TM_DEMO_MODE", "false") == "true",
		RedactPayloads: env("TM_REDACT_PAYLOADS", "true") != "false",
		TrustedTools:   set(splitList(env("TM_TRUSTED_TOOLS", ""))),
		LogLevel:       level(env("TM_LOG_LEVEL", "info")),
		ReplayModel:    env("TM_REPLAY_MODEL", "gpt-4o-mini"),
		OpenAIKey:      env("OPENAI_API_KEY", ""),
	}
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("TM_DATABASE_URL is not set")
	}
	if c.IngestKey == "" {
		return c, fmt.Errorf("TM_INGEST_KEY is not set")
	}
	return c, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func set(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, i := range items {
		m[i] = true
	}
	return m
}

func level(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
