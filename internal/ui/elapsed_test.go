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
