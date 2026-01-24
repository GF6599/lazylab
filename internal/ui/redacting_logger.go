package ui

import (
	"context"
	"log/slog"
	"regexp"
)

// redactingHandler wraps an slog.Handler to redact sensitive information from logs.
type redactingHandler struct {
	handler slog.Handler
}

// NewRedactingHandler creates a handler that redacts sensitive tokens from log output.
func NewRedactingHandler(h slog.Handler) slog.Handler {
	return &redactingHandler{handler: h}
}

var (
	// GitLab token patterns (glpat-, gloas-, gldt-, etc.) - minimum 6 chars after prefix
	tokenPattern = regexp.MustCompile(`gl[a-z]{2,4}-[a-zA-Z0-9_-]{6,}`)
	// Authorization headers (must come before bearer to catch full header value)
	authHeaderPattern = regexp.MustCompile(`[Aa]uthorization:\s+[^\s]+(\s+[^\s]+)*`)
	// Generic bearer tokens
	bearerPattern = regexp.MustCompile(`[Bb]earer\s+[a-zA-Z0-9_-]+`)
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

// RedactToken is a helper function to redact tokens in error messages.
// This can be used by other parts of the codebase to manually redact sensitive data.
func RedactToken(s string) string {
	return redactString(s)
}
