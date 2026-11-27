// Package config loads process configuration from environment variables into a
// typed struct and validates it once at startup, so a misconfigured process
// fails fast instead of at first use.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"time"
)

// Config is the api binary's configuration.
type Config struct {
	HTTPAddr        string        // HTTP_ADDR, default ":8080"
	DatabaseURL     string        // DATABASE_URL, required
	LogLevel        slog.Level    // LOG_LEVEL: debug|info|warn|error, default info
	ShutdownTimeout time.Duration // SHUTDOWN_TIMEOUT, default 15s
	HoldTTL         time.Duration // HOLD_TTL, how long a held booking blocks a slot, default 10m
}

// Load reads configuration through getenv (os.Getenv in production) and
// returns every validation problem at once, joined.
func Load(getenv func(string) string) (Config, error) {
	var errs []error
	get := func(key, def string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return def
	}
	dur := func(key, def string, lo, hi time.Duration) time.Duration {
		raw := get(key, def)
		d, err := time.ParseDuration(raw)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("%s: %q is not a duration", key, raw))
		case d < lo || d > hi:
			errs = append(errs, fmt.Errorf("%s: %s must be between %s and %s", key, d, lo, hi))
		}
		return d
	}

	cfg := Config{
		HTTPAddr:        get("HTTP_ADDR", ":8080"),
		DatabaseURL:     getenv("DATABASE_URL"),
		ShutdownTimeout: dur("SHUTDOWN_TIMEOUT", "15s", time.Second, 5*time.Minute),
		HoldTTL:         dur("HOLD_TTL", "10m", time.Minute, 24*time.Hour),
	}

	if _, _, err := net.SplitHostPort(cfg.HTTPAddr); err != nil {
		errs = append(errs, fmt.Errorf("HTTP_ADDR: %q must be host:port", cfg.HTTPAddr))
	}

	// Never echo DATABASE_URL: it usually contains a password.
	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL: required"))
	} else if u, err := url.Parse(cfg.DatabaseURL); err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		errs = append(errs, errors.New("DATABASE_URL: must be a postgres:// URL"))
	}

	if err := cfg.LogLevel.UnmarshalText([]byte(get("LOG_LEVEL", "info"))); err != nil {
		errs = append(errs, fmt.Errorf("LOG_LEVEL: %q must be debug, info, warn or error", getenv("LOG_LEVEL")))
	}

	if err := errors.Join(errs...); err != nil {
		return Config{}, fmt.Errorf("invalid configuration:\n%w", err)
	}
	return cfg, nil
}
