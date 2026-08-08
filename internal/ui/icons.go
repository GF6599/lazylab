// icons.go provides Unicode visual indicators for pipeline statuses, project
// metadata, and file types. These replace text labels with compact glyphs that
// are scannable at a glance in the TUI. The pipeline status icons mirror
// GitLab's web UI conventions (checkmark for success, X for failed, etc.).

package ui

import (
	"fmt"
	"strings"
	"time"
)

// Every glyph below is East Asian Neutral and is not an emoji, so it measures
// one cell everywhere. Two classes of glyph do not, and both tear a pane by
// pushing its rows one cell past the border:
//
//   - An East Asian Ambiguous glyph, which a terminal in a CJK locale draws at
//     two cells. This rules out the obvious ● ○ ▶ — and the middle dot ·.
//   - An emoji Unicode leaves in text presentation by default, such as ⏱ or ▶,
//     which Go measures at one cell and an emoji-presenting terminal draws at
//     two.
//
// Neither property is sufficient on its own. A glyph can satisfy both and still
// draw past its cell, which no width library can report: ⬤ U+2B24 measures one
// cell and overruns the column in Agave. Look at a new icon rendered before
// trusting its properties.
//
// The wide emoji further down are deliberate: they carry default emoji
// presentation, so every terminal agrees they are two cells.

// Pipeline status icons — each maps to a GitLab CI pipeline/job status string.
const (
	iconSuccess    = "✓"
	iconFailed     = "✗"
	iconRunning    = "◉"
	iconPending    = "◦"
	iconCanceled   = "⊘"
	iconSkipped    = "−"
	iconManual     = "►"
	iconBlocked    = "◧"
	iconUnknown    = "?"
	iconNoPipeline = "∅"
)

// General icons
const (
	iconProject       = "📦"
	iconPrivate       = "🔒"
	iconPublic        = "🌐"
	iconInternal      = "🏢"
	iconStar          = "⭐"
	iconClock         = "◷"
	iconSelection     = "►"
	iconTreeCollapsed = "▸"
	iconTreeExpanded  = "▾"
)

// pipelineStatusIcon maps a GitLab status string to its Unicode indicator.
// Handles all known statuses; unknown values get "?".
func pipelineStatusIcon(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success":
		return iconSuccess
	case "failed":
		return iconFailed
	case "running":
		return iconRunning
	case "pending", "created", "waiting_for_resource", "scheduled", "preparing":
		return iconPending
	case "canceled":
		return iconCanceled
	case "skipped":
		return iconSkipped
	case "manual":
		return iconManual
	case "blocked":
		return iconBlocked
	default:
		return iconUnknown
	}
}

// visibilityIcon returns an icon for project visibility
func visibilityIcon(visibility string) string {
	switch strings.ToLower(visibility) {
	case "private":
		return iconPrivate
	case "public":
		return iconPublic
	case "internal":
		return iconInternal
	default:
		return iconProject
	}
}

// formatTimeAgo formats a timestamp as "time ago" string
func formatTimeAgo(t time.Time) string {
	if t.IsZero() {
		return "never"
	}

	ago := time.Since(t)

	switch {
	case ago < time.Minute:
		return "just now"
	case ago < time.Hour:
		mins := int(ago.Minutes())
		if mins == 1 {
			return "1m ago"
		}
		return fmt.Sprintf("%dm ago", mins)
	case ago < 24*time.Hour:
		hours := int(ago.Hours())
		if hours == 1 {
			return "1h ago"
		}
		return fmt.Sprintf("%dh ago", hours)
	case ago < 7*24*time.Hour:
		days := int(ago.Hours() / 24)
		if days == 1 {
			return "1d ago"
		}
		return fmt.Sprintf("%dd ago", days)
	case ago < 30*24*time.Hour:
		weeks := int(ago.Hours() / 24 / 7)
		if weeks == 1 {
			return "1w ago"
		}
		return fmt.Sprintf("%dw ago", weeks)
	default:
		months := int(ago.Hours() / 24 / 30)
		if months == 1 {
			return "1mo ago"
		}
		return fmt.Sprintf("%dmo ago", months)
	}
}

// formatBytes formats a byte count into a human-readable string.
func formatBytes(b int) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
