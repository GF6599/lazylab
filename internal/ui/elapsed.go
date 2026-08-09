package ui

import (
	"fmt"
	"time"

	"github.com/GF6599/lazylab/internal/gitlab"
)

// formatDuration draws a span as a stopwatch, not as an age. formatTimeAgo next door already
// does the latter, and "1m ago" is the wrong reading for work still going on.
func formatDuration(d time.Duration) string {
	// A start time comes from GitLab and the moment it is measured against comes from this
	// machine, so a clock a few seconds behind the server's is the one way d goes negative.
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// A zero start is what GitLab sends for work it has accepted but not begun.
func formatElapsed(start, now time.Time) string {
	if start.IsZero() {
		return ""
	}
	return formatDuration(now.Sub(start))
}

// jobElapsed prefers the duration GitLab reports, which it fills in only once a job stops. A
// running job has none, so it is timed from its own start instead.
func jobElapsed(job gitlab.PipelineJob, now time.Time) string {
	if job.Duration > 0 {
		return formatDuration(time.Duration(job.Duration * float64(time.Second)))
	}
	return formatElapsed(job.StartedAt, now)
}
