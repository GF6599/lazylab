package redacting

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
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
		{
			// CI/CD job token: long prefix, mixed case.
			name:  "GitLab CI bot token",
			input: "found glcbt-abc123def456",
			want:  "found [REDACTED-TOKEN]",
		},
		{
			// Hypothetical short-prefix variant that the previous
			// {2,4} bound would have missed.
			name:  "single-char prefix variant",
			input: "key=glx-abc123def",
			want:  "key=[REDACTED-TOKEN]",
		},
		{
			// Hypothetical uppercase variant (e.g. GLPAT-) that previous
			// [a-z] character class would have missed.
			name:  "uppercase prefix variant",
			input: "leak: glPAT-abc123def456",
			want:  "leak: [REDACTED-TOKEN]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Redact(tt.input)
			if got != tt.want {
				t.Errorf("Redact() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHandler(t *testing.T) {
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
					// Remove timestamp for consistent test output.
					if a.Key == slog.TimeKey {
						return slog.Attr{}
					}
					return a
				},
			})
			handler := NewHandler(baseHandler)
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
