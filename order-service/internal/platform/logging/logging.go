// Package logging provides the structured, JSON-only logger used by every
// FundKit service. Logs are machine-parseable by design: in a distributed
// system the log line is a queryable event, not a sentence for a human.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/dhanush-cn/fundkit/order-service/internal/platform/trace"
)

// contextHandler decorates every record with the correlation id held in the
// context, so a request can be reconstructed end-to-end from logs alone
// without each call site remembering to attach the field.
type contextHandler struct {
	slog.Handler
}

func (h contextHandler) Handle(ctx context.Context, record slog.Record) error {
	if id := trace.FromContext(ctx); id != "" {
		record.AddAttrs(slog.String(trace.HeaderKey, id))
	}
	return h.Handler.Handle(ctx, record)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{Handler: h.Handler.WithGroup(name)}
}

// New builds a JSON logger tagged with the service name and installs it as the
// process-wide default so third-party packages inherit the same format.
func New(service, level string) *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLevel(level),
	})

	logger := slog.New(contextHandler{Handler: handler}).With(slog.String("service", service))
	slog.SetDefault(logger)
	return logger
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
