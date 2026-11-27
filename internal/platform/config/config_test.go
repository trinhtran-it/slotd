package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{"DATABASE_URL": "postgres://u:p@localhost:5432/db"}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := Config{
		HTTPAddr:        ":8080",
		DatabaseURL:     "postgres://u:p@localhost:5432/db",
		LogLevel:        slog.LevelInfo,
		ShutdownTimeout: 15 * time.Second,
		HoldTTL:         10 * time.Minute,
	}
	if cfg != want {
		t.Fatalf("Load() = %+v, want %+v", cfg, want)
	}
}

func TestLoad_Overrides(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"DATABASE_URL":     "postgresql://localhost/db",
		"HTTP_ADDR":        "127.0.0.1:9000",
		"LOG_LEVEL":        "debug",
		"SHUTDOWN_TIMEOUT": "30s",
		"HOLD_TTL":         "5m",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPAddr != "127.0.0.1:9000" || cfg.LogLevel != slog.LevelDebug ||
		cfg.ShutdownTimeout != 30*time.Second || cfg.HoldTTL != 5*time.Minute {
		t.Fatalf("Load() = %+v", cfg)
	}
}

func TestLoad_Invalid(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantMsg []string
	}{
		{
			name:    "missing database url",
			env:     map[string]string{},
			wantMsg: []string{"DATABASE_URL: required"},
		},
		{
			name:    "wrong scheme",
			env:     map[string]string{"DATABASE_URL": "mysql://secret@host/db"},
			wantMsg: []string{"DATABASE_URL: must be a postgres:// URL"},
		},
		{
			name: "reports every problem at once",
			env: map[string]string{
				"DATABASE_URL":     "postgres://localhost/db",
				"HTTP_ADDR":        "8080",
				"LOG_LEVEL":        "loud",
				"SHUTDOWN_TIMEOUT": "soon",
				"HOLD_TTL":         "1s",
			},
			wantMsg: []string{"HTTP_ADDR", "LOG_LEVEL", "SHUTDOWN_TIMEOUT", "HOLD_TTL"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(env(tt.env))
			if err == nil {
				t.Fatal("Load() error = nil, want error")
			}
			for _, m := range tt.wantMsg {
				if !strings.Contains(err.Error(), m) {
					t.Errorf("error %q does not mention %q", err, m)
				}
			}
			if strings.Contains(err.Error(), "secret") {
				t.Errorf("error leaks DATABASE_URL contents: %q", err)
			}
		})
	}
}
