package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/x/ansi"

	"github.com/GF6599/lazylab/internal/gitlab"
)

func jobDetailModel(job gitlab.PipelineJob) Model {
	jobsCache := NewAsyncCache[int, []gitlab.PipelineJob]()
	jobsCache.Set(10, []gitlab.PipelineJob{job})
	return Model{
		mode:   modeMultiPanel,
		width:  120,
		height: 40,
		focus:  FocusState{Active: PanelStages},
		pipelineView: pipelineViewState{
			project:     gitlab.ProjectNode{ID: 1},
			pipelines:   []gitlab.PipelineSummary{{ID: 10, Ref: "main", Status: "running"}},
			selected:    0,
			logJobID:    job.ID,
			jobs:        jobsCache,
			stages:      NewAsyncCache[int, []gitlab.PipelineStage](),
			logs:        NewAsyncCache[int, string](),
			bridges:     NewAsyncCache[int, []gitlab.PipelineBridge](),
			logViewport: viewport.New(60, 20),
		},
	}
}

// TestJobDetail_ShowsHowLongARunningJobHasBeenGoing: a running job reports the time it has taken
// so far.
// Given a job that started two minutes ago and is still running, when the detail pane is drawn,
// then it reports an elapsed time.
// Why it matters: GitLab reports a duration only once a job stops, so the pane has a time to show
// exactly when the wait is over and nothing to show for the whole time the user is waiting.
func TestJobDetail_ShowsHowLongARunningJobHasBeenGoing(t *testing.T) {
	// Given: a job that started two minutes ago and is still running, so GitLab reports no duration
	m := jobDetailModel(gitlab.PipelineJob{
		ID:        1,
		Name:      "compile",
		Stage:     "build",
		Status:    "running",
		StartedAt: time.Now().Add(-2 * time.Minute),
	})

	// When: the detail pane is drawn
	pane := ansi.Strip(renderPipelineLogContent(m, 80, 24))

	// Then: it reports how long the job has been going
	if !strings.Contains(pane, "2m") {
		t.Errorf("the pane shows no elapsed time for a job running two minutes:\n%s", pane)
	}
}

// TestJobDetail_ReportsTheFinalTimeOnceAJobStops: a finished job reports a time that holds still.
// Given a job that finished after ninety seconds, when the detail pane is drawn twice a moment
// apart, then both draws report the same time.
// Why it matters: a stopwatch that keeps counting after the job stops reports work that is not
// happening, which is the opposite of what the number is for.
func TestJobDetail_ReportsTheFinalTimeOnceAJobStops(t *testing.T) {
	// Given: a job that ran for ninety seconds and finished
	started := time.Now().Add(-10 * time.Minute)
	m := jobDetailModel(gitlab.PipelineJob{
		ID:         1,
		Name:       "compile",
		Stage:      "build",
		Status:     "success",
		StartedAt:  started,
		FinishedAt: started.Add(90 * time.Second),
		Duration:   90,
	})

	// When: the detail pane is drawn twice a moment apart
	first := ansi.Strip(renderPipelineLogContent(m, 80, 24))
	second := ansi.Strip(renderPipelineLogContent(m, 80, 24))

	// Then: the time it reports holds still, and it is the time the job took
	if !strings.Contains(first, "1m 30s") {
		t.Errorf("the pane does not report the 90 seconds the job took:\n%s", first)
	}
	if timeLine(first) != timeLine(second) {
		t.Errorf("the reported time moved after the job finished: %q then %q",
			timeLine(first), timeLine(second))
	}
}

func timeLine(pane string) string {
	for _, line := range strings.Split(pane, "\n") {
		if strings.Contains(line, "Elapsed:") {
			return strings.TrimRight(line, " ")
		}
	}
	return ""
}

// TestFormatElapsed_ReadsAsAStopwatchAtEveryScale: elapsed time is drawn at a scale a reader can
// use.
// Given a start and a later moment, when the gap is formatted, then it reads as a stopwatch at
// seconds, minutes and hours, and a clock that disagrees never produces a negative time.
// Why it matters: this is drawn against a job the user is waiting on, so a gap the wrong side of
// zero reads as a fault in the app rather than in the two clocks it sits between.
func TestFormatElapsed_ReadsAsAStopwatchAtEveryScale(t *testing.T) {
	start := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name string
		now  time.Time
		want string
	}{
		{"the moment it starts", start, "0s"},
		{"under a minute", start.Add(9 * time.Second), "9s"},
		{"a minute and change", start.Add(83 * time.Second), "1m 23s"},
		{"exactly an hour", start.Add(time.Hour), "1h 0m"},
		{"hours and minutes", start.Add(2*time.Hour + 7*time.Minute + 30*time.Second), "2h 7m"},
		// Given: a clock behind the server's, which is the only way now precedes start
		{"a clock running behind", start.Add(-3 * time.Second), "0s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// When: the gap is formatted
			got := formatElapsed(start, tc.now)

			// Then: it reads as a stopwatch
			if got != tc.want {
				t.Errorf("formatElapsed(start, start%+v) = %q, want %q",
					tc.now.Sub(start), got, tc.want)
			}
		})
	}
}

// TestFormatElapsed_SaysNothingWithoutAStart: a job GitLab has not started shows no time at all.
// Given a zero start time, when it is formatted, then the result is empty.
// Why it matters: a queued job has no start, and counting from the zero time would draw a figure
// in the tens of thousands of hours beside it.
func TestFormatElapsed_SaysNothingWithoutAStart(t *testing.T) {
	// Given: a job GitLab has not started, so its start time is the zero value
	// When: it is formatted
	got := formatElapsed(time.Time{}, time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC))

	// Then: nothing is drawn
	if got != "" {
		t.Errorf("a job with no start time drew %q, want no time at all", got)
	}
}
