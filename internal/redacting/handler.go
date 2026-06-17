// Package redacting strips GitLab tokens and other secret-shaped values
// from strings before they reach user-visible output (logs, stderr, TUI
// status lines, CLI tables). It exposes a slog.Handler decorator
// ([NewHandler]) for the logging pipeline and a plain [Redact] function
// for ad-hoc call sites that surface raw error text from the GitLab SDK.
//
// It is a cross-cutting utility and is intentionally importable by any
// layer — including the UI — because the upstream error origin (the
// GitLab SDK) cannot be controlled. Treat it like log/slog: a leaf
// dependency with no layering opinion.
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
//
// Handler holds no shared mutable state of its own, so it is as safe for
// concurrent use as the wrapped handler it decorates. Redaction applies
// only to the record message and to string- and group-kinded attribute
// values (groups recursively); all other value kinds (ints, bools,
// durations, times, etc.) pass through untouched.
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

// Enabled reports whether the wrapped handler is enabled for level; it
// delegates directly without altering the decision.
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

// Handle redacts the record's message and attribute values, then forwards
// a rewritten copy to the wrapped handler. The returned error is the
// wrapped handler's error verbatim (not wrapped); redaction itself never
// fails, so any non-nil error originates downstream.
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

// WithAttrs redacts attr values up front, at bind time, before handing
// them to the wrapped handler — not when records are later emitted. This
// means a secret captured in a pre-bound attribute is scrubbed once here
// and the wrapped handler only ever stores the redacted form.
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

// WithGroup opens a named attribute group on the wrapped handler. Unlike
// WithAttrs, the group name is passed through unredacted: group names are
// caller-supplied structural labels, not log payload, so they are not
// expected to carry secrets and are left intact.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{handler: h.handler.WithGroup(name)}
}
