package proxemby

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

const (
	defaultLogLevel  = "info"
	defaultLogFormat = "text"
)

type LoggingConfig struct {
	Level  slog.Level
	Format string
	Time   bool
}

func NewLogger(cfg LoggingConfig, w io.Writer) (*slog.Logger, error) {
	opts := &slog.HandlerOptions{
		Level: cfg.Level,
	}
	if !cfg.Time {
		opts.ReplaceAttr = func(groups []string, attr slog.Attr) slog.Attr {
			if len(groups) == 0 && attr.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return attr
		}
	}

	switch cfg.Format {
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	default:
		return nil, fmt.Errorf("log format must be text or json")
	}
}

func parseLoggingConfig(levelValue, formatValue string, timeValue bool) (LoggingConfig, error) {
	level, err := parseLogLevel(levelValue)
	if err != nil {
		return LoggingConfig{}, err
	}
	formatValue = strings.ToLower(strings.TrimSpace(formatValue))
	switch formatValue {
	case "text", "json":
	default:
		return LoggingConfig{}, fmt.Errorf("log format must be text or json")
	}
	return LoggingConfig{
		Level:  level,
		Format: formatValue,
		Time:   timeValue,
	}, nil
}

func parseLogLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("log level must be debug, info, warn, or error")
	}
}

type slogWriter struct {
	logger *slog.Logger
	level  slog.Level
}

func (w slogWriter) Write(p []byte) (int, error) {
	text := strings.TrimSpace(string(p))
	if text != "" {
		w.logger.Log(context.Background(), w.level, text)
	}
	return len(p), nil
}
