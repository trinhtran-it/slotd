package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestContextAttrsAreLogged(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, slog.LevelInfo).With("component", "test")

	ctx := WithAttrs(t.Context(), slog.String("request_id", "abc"))
	ctx = WithAttrs(ctx, slog.String("user", "u1"))
	logger.InfoContext(ctx, "hello")
	logger.DebugContext(ctx, "dropped below level")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("want exactly one JSON record, got %q: %v", buf.String(), err)
	}
	for k, want := range map[string]string{"msg": "hello", "request_id": "abc", "user": "u1", "component": "test"} {
		if rec[k] != want {
			t.Errorf("record[%q] = %v, want %q", k, rec[k], want)
		}
	}
}

func TestWithAttrsDoesNotMutateParent(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, slog.LevelInfo)

	parent := WithAttrs(t.Context(), slog.String("a", "1"))
	_ = WithAttrs(parent, slog.String("b", "2"))
	logger.InfoContext(parent, "parent")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	if _, ok := rec["b"]; ok {
		t.Fatalf("child attribute leaked into parent context: %v", rec)
	}
}
