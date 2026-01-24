package ui

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestRedactString(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "GitLab personal access token",
			input: "error: glpat-abcdefghijklmnopqrstuvwxyz",
			want:  "error: [REDACTED-TOKEN]",
		},
		{
			name:  "GitLab OAuth token",
			input: "failed with gloas-1234567890abcdefghij",
			want:  "failed with [REDACTED-TOKEN]",
		},
		{
			name:  "Bearer token in header",
			input: "Authorization: Bearer abc123xyz",
			want:  "Authorization: [REDACTED]",
		},
		{
			name:  "Bearer token standalone",
			input: "token: Bearer abc123xyz456",
			want:  "token: Bearer [REDACTED]",
		},
		{
			name:  "Authorization header",
			input: "Authorization: glpat-secret123456789",
			want:  "Authorization: [REDACTED]",
		},
		{
			name:  "Multiple tokens",
			input: "tokens: glpat-abc123 and gldt-xyz789",
			want:  "tokens: [REDACTED-TOKEN] and [REDACTED-TOKEN]",
		},
		{
			name:  "No sensitive data",
			input: "normal log message",
			want:  "normal log message",
		},
		{
			name:  "Token in URL",
			input: "GET https://gitlab.com/api?token=glpat-secret",
			want:  "GET https://gitlab.com/api?token=[REDACTED-TOKEN]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactString(tt.input)
			if got != tt.want {
				t.Errorf("redactString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRedactingHandler(t *testing.T) {
	tests := []struct {
		name            string
		message         string
		attrs           []slog.Attr
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:            "redacts token in message",
			message:         "API error with glpat-secrettoken123456",
			wantContains:    []string{"[REDACTED-TOKEN]"},
			wantNotContains: []string{"glpat-secrettoken123456"},
		},
		{
			name:    "redacts token in error attribute",
			message: "request failed",
			attrs: []slog.Attr{
				slog.String("err", "unauthorized: token glpat-secret123456789 is invalid"),
			},
			wantContains:    []string{"[REDACTED-TOKEN]"},
			wantNotContains: []string{"glpat-secret123456789"},
		},
		{
			name:    "preserves non-sensitive data",
			message: "connection established",
			attrs: []slog.Attr{
				slog.String("host", "gitlab.com"),
				slog.Int("port", 443),
			},
			wantContains:    []string{"gitlab.com", "443"},
			wantNotContains: []string{"[REDACTED"},
		},
		{
			name:    "redacts authorization header",
			message: "auth failed",
			attrs: []slog.Attr{
				slog.String("header", "Authorization: Bearer abc123xyz"),
			},
			wantContains:    []string{"Authorization: [REDACTED]"},
			wantNotContains: []string{"abc123xyz", "Bearer"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			baseHandler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
				ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
					// Remove timestamp for consistent test output
					if a.Key == slog.TimeKey {
						return slog.Attr{}
					}
					return a
				},
			})
			handler := NewRedactingHandler(baseHandler)
			logger := slog.New(handler)

			logger.LogAttrs(context.Background(), slog.LevelInfo, tt.message, tt.attrs...)

			output := buf.String()

			for _, want := range tt.wantContains {
				if !strings.Contains(output, want) {
					t.Errorf("output should contain %q, got: %s", want, output)
				}
			}

			for _, notWant := range tt.wantNotContains {
				if strings.Contains(output, notWant) {
					t.Errorf("output should NOT contain %q, got: %s", notWant, output)
				}
			}
		})
	}
}

func TestRedactToken(t *testing.T) {
	input := "error message with glpat-abc123def456ghi789jkl"
	result := RedactToken(input)

	if strings.Contains(result, "glpat-") {
		t.Error("RedactToken should remove token patterns")
	}

	if !strings.Contains(result, "[REDACTED-TOKEN]") {
		t.Error("RedactToken should replace with [REDACTED-TOKEN]")
	}
}
