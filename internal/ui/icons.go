package ui

import (
	"fmt"
	"strings"
	"time"
)

// Pipeline status icons
const (
	iconSuccess  = "✓"
	iconFailed   = "✗"
	iconRunning  = "●"
	iconPending  = "○"
	iconCanceled = "⊘"
	iconSkipped  = "−"
	iconUnknown  = "?"
)

// General icons
const (
	iconProject    = "📦"
	iconFolder     = "📁"
	iconFile       = "📄"
	iconPrivate    = "🔒"
	iconPublic     = "🌐"
	iconInternal   = "🏢"
	iconStar       = "⭐"
	iconClock      = "⏱"
	iconBranch     = "🌿"
	iconCommit     = "●"
	iconTag        = "🏷"
	iconLoading    = "⟳"
	iconRecent     = "🕐"
	iconBreadcrumb = "›"
)

// pipelineStatusIcon returns a Unicode icon for the pipeline status
func pipelineStatusIcon(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success":
		return iconSuccess
	case "failed":
		return iconFailed
	case "running":
		return iconRunning
	case "pending", "created", "waiting_for_resource", "scheduled":
		return iconPending
	case "canceled":
		return iconCanceled
	case "skipped":
		return iconSkipped
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

// formatDuration formats a duration in a human-readable way
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
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

// formatProjectCount formats a count of projects
func formatProjectCount(visible, total int) string {
	if visible == total {
		return fmt.Sprintf("%d projects", total)
	}
	return fmt.Sprintf("%d of %d projects", visible, total)
}

// makeBreadcrumb creates a breadcrumb path string
func makeBreadcrumb(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " "+iconBreadcrumb+" ")
}
