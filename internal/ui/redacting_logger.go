// redacting_logger.go provides a security-critical slog.Handler that prevents
// GitLab tokens and bearer credentials from leaking into log files or stderr.
//
// Because the TUI renders on an alternate screen, log output goes to stderr or
// a file. Without redaction, debug-level logs from the HTTP client would expose
// PRIVATE-TOKEN headers and glpat-* values. This handler intercepts every log
// record — messages, attributes, and nested groups — and applies regex-based
// scrubbing before forwarding to the wrapped handler.
//
// The regex patterns are intentionally broad (matching any gl*-prefixed token
// with 6+ characters) to catch future GitLab token formats without code changes.

package ui

import (
	"context"
	"log/slog"
	"regexp"
)

// redactingHandler wraps an slog.Handler to scrub sensitive credentials.
// It implements the full slog.Handler interface so it can be used as a
// drop-in decorator around any underlying handler (text, JSON, etc.).
type redactingHandler struct {
	handler slog.Handler
}

// NewRedactingHandler wraps h so that every log record has GitLab tokens,
// Authorization headers, and bearer credentials replaced with [REDACTED]
// placeholders before reaching the underlying handler.
func NewRedactingHandler(h slog.Handler) slog.Handler {
	return &redactingHandler{handler: h}
}

// Redaction patterns are applied in order: auth headers first (most specific),
// then bearer tokens, then bare GitLab tokens. Order matters because
// "Authorization: Bearer glpat-xxx" should become "Authorization: [REDACTED]",
// not "Authorization: Bearer [REDACTED-TOKEN]".
var (
	// tokenPattern matches any GitLab-prefixed token: glpat-, gldt-, gloas-,
	// glcbt-, and any future short-prefix variant GitLab adds. The prefix
	// allows 1-8 alphanumeric characters (mixed case) after "gl", followed by
	// "-" and 6+ token characters. Widened from the previous gl[a-z]{2,4}- so
	// uppercase variants and single-char-prefix tokens don't slip through.
	tokenPattern      = regexp.MustCompile(`gl[a-zA-Z0-9]{1,8}-[a-zA-Z0-9_-]{6,}`)
	authHeaderPattern = regexp.MustCompile(`[Aa]uthorization:\s+[^\s]+(\s+[^\s]+)*`)
	bearerPattern     = regexp.MustCompile(`[Bb]earer\s+[a-zA-Z0-9_-]+`)
)

// redactString sanitizes sensitive information from a string.
func redactString(s string) string {
	// Order matters: auth header first, then bearer, then tokens
	s = authHeaderPattern.ReplaceAllString(s, "Authorization: [REDACTED]")
	s = bearerPattern.ReplaceAllString(s, "Bearer [REDACTED]")
	s = tokenPattern.ReplaceAllString(s, "[REDACTED-TOKEN]")
	return s
}

// redactValue recursively redacts sensitive information from slog attribute values.
func redactValue(v slog.Value) slog.Value {
	switch v.Kind() {
	case slog.KindString:
		return slog.StringValue(redactString(v.String()))
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

func (h *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

func (h *redactingHandler) Handle(ctx context.Context, record slog.Record) error {
	// Redact the message
	record.Message = redactString(record.Message)

	// Redact attributes
	var redactedAttrs []slog.Attr
	record.Attrs(func(a slog.Attr) bool {
		redactedAttrs = append(redactedAttrs, slog.Attr{
			Key:   a.Key,
			Value: redactValue(a.Value),
		})
		return true
	})

	// Create new record with redacted attributes
	newRecord := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	newRecord.AddAttrs(redactedAttrs...)

	return h.handler.Handle(ctx, newRecord)
}

func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	// Redact attributes before passing to wrapped handler
	redacted := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		redacted[i] = slog.Attr{
			Key:   a.Key,
			Value: redactValue(a.Value),
		}
	}
	return &redactingHandler{handler: h.handler.WithAttrs(redacted)}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{handler: h.handler.WithGroup(name)}
}

// RedactToken applies the same redaction rules used by the log handler.
// Call this when surfacing error messages in the TUI that might contain tokens.
func RedactToken(s string) string {
	return redactString(s)
}
