package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestLoggerJSONAndTimeOptions(t *testing.T) {
	var logs bytes.Buffer
	logger, err := NewLogger(Config{
		Level:  slog.LevelDebug,
		Format: "json",
		Time:   false,
	}, &logs)
	if err != nil {
		t.Fatal(err)
	}
	logger.Debug("hello", "answer", 42)

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &entry); err != nil {
		t.Fatalf("json log = %q, err = %v", logs.String(), err)
	}
	if _, ok := entry["time"]; ok {
		t.Fatalf("log entry contains time: %v", entry)
	}
	if entry["level"] != "DEBUG" || entry["msg"] != "hello" || entry["answer"] != float64(42) {
		t.Fatalf("unexpected log entry: %v", entry)
	}
}
