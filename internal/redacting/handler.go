// Package redacting provides a security-critical slog.Handler that prevents
// GitLab tokens and bearer credentials from leaking into log files or stderr.
//
// The package is intentionally tiny and TUI-free: it is imported by both the
// CLI (cmd/lazylab) and the TUI (internal/ui), and pulling it out of
// internal/ui keeps the CLI binary from dragging in the entire Bubble Tea
// dependency graph for what is fundamentally a log-handler concern.
//
// The regex patterns are intentionally broad (matching any gl*-prefixed
// token with 6+ characters) to catch future GitLab token formats without
// code changes.
package redacting

import (
	"context"
	"log/slog"
	"regexp"
)

// Handler wraps an slog.Handler to scrub sensitive credentials. It
// implements the full slog.Handler interface so it can be used as a
// drop-in decorator around any underlying handler (text, JSON, etc.).
type Handler struct {
	handler slog.Handler
}

// NewHandler wraps h so that every log record has GitLab tokens,
// Authorization headers, and bearer credentials replaced with [REDACTED]
// placeholders before reaching the underlying handler.
func NewHandler(h slog.Handler) slog.Handler {
	return &Handler{handler: h}
}

// Redaction patterns are applied in order: auth headers first (most
// specific), then bearer tokens, then bare GitLab tokens. Order matters
// because "Authorization: Bearer glpat-xxx" should become
// "Authorization: [REDACTED]", not "Authorization: Bearer [REDACTED-TOKEN]".
var (
	// tokenPattern matches any GitLab-prefixed token: glpat-, gldt-,
	// gloas-, glcbt-, and any future short-prefix variant GitLab adds.
	// The prefix allows 1-8 alphanumeric characters (mixed case) after
	// "gl", followed by "-" and 6+ token characters. Widened from the
	// previous gl[a-z]{2,4}- so uppercase variants and single-char-prefix
	// tokens don't slip through.
	tokenPattern      = regexp.MustCompile(`gl[a-zA-Z0-9]{1,8}-[a-zA-Z0-9_-]{6,}`)
	authHeaderPattern = regexp.MustCompile(`[Aa]uthorization:\s+[^\s]+(\s+[^\s]+)*`)
	bearerPattern     = regexp.MustCompile(`[Bb]earer\s+[a-zA-Z0-9_-]+`)
)

// Redact sanitizes sensitive information from a string. Callers outside
// the slog pipeline (e.g. TUI status lines that surface raw error text)
// can use this directly to apply the same scrub rules.
func Redact(s string) string {
	// Order matters: auth header first, then bearer, then tokens.
	s = authHeaderPattern.ReplaceAllString(s, "Authorization: [REDACTED]")
	s = bearerPattern.ReplaceAllString(s, "Bearer [REDACTED]")
	s = tokenPattern.ReplaceAllString(s, "[REDACTED-TOKEN]")
	return s
}

// redactValue recursively redacts sensitive information from slog
// attribute values.
func redactValue(v slog.Value) slog.Value {
	switch v.Kind() {
	case slog.KindString:
		return slog.StringValue(Redact(v.String()))
	case slog.KindGroup:
		attrs := v.Group()
		redacted := make([]slog.Attr, len(attrs))
		for i, a := range attrs {
			redacted[i] = slog.Attr{
				Key:   a.Key,
				Value: redactValue(a.Value),
			}
		}
		return slog.GroupValue(redacted...)
	default:
		return v
	}
}

func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

func (h *Handler) Handle(ctx context.Context, record slog.Record) error {
	// Redact the message.
	record.Message = Redact(record.Message)

	// Redact attributes.
	var redactedAttrs []slog.Attr
	record.Attrs(func(a slog.Attr) bool {
		redactedAttrs = append(redactedAttrs, slog.Attr{
			Key:   a.Key,
			Value: redactValue(a.Value),
		})
		return true
	})

	// Create new record with redacted attributes.
	newRecord := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	newRecord.AddAttrs(redactedAttrs...)

	return h.handler.Handle(ctx, newRecord)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	// Redact attributes before passing to wrapped handler.
	redacted := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		redacted[i] = slog.Attr{
			Key:   a.Key,
			Value: redactValue(a.Value),
		}
	}
	return &Handler{handler: h.handler.WithAttrs(redacted)}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{handler: h.handler.WithGroup(name)}
}
