package gitlab

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestParsePipelineURL is a table over the URL shapes the CLI is likely
// to see — copy-paste from a browser, Slack unfurl with `?ref_type=`,
// hash anchor to a job line, etc. Failing here means the "paste from
// chat" flow breaks for that variant.
func TestParsePipelineURL(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantPath   string
		wantPipeID int
		wantErr    bool
	}{
		{
			name:       "gitlab.com plain",
			input:      "https://gitlab.com/foo/bar/-/pipelines/12345",
			wantPath:   "foo/bar",
			wantPipeID: 12345,
		},
		{
			name:       "self-hosted with subgroup",
			input:      "https://gitlab.mycompany.com/group/sub/project/-/pipelines/9876",
			wantPath:   "group/sub/project",
			wantPipeID: 9876,
		},
		{
			name:       "trailing slash",
			input:      "https://gitlab.com/foo/bar/-/pipelines/100/",
			wantPath:   "foo/bar",
			wantPipeID: 100,
		},
		{
			name:       "with builds suffix",
			input:      "https://gitlab.com/foo/bar/-/pipelines/200/builds",
			wantPath:   "foo/bar",
			wantPipeID: 200,
		},
		{
			name:       "with query string",
			input:      "https://gitlab.com/foo/bar/-/pipelines/300?ref_type=heads",
			wantPath:   "foo/bar",
			wantPipeID: 300,
		},
		{
			name:       "with hash anchor",
			input:      "https://gitlab.com/foo/bar/-/pipelines/400#job-99",
			wantPath:   "foo/bar",
			wantPipeID: 400,
		},
		{
			name:    "not a pipeline url",
			input:   "https://gitlab.com/foo/bar",
			wantErr: true,
		},
		{
			name:    "non-numeric id",
			input:   "https://gitlab.com/foo/bar/-/pipelines/abc",
			wantErr: true,
		},
		{
			name:    "empty",
			input:   "",
			wantErr: true,
		},
		{
			name:    "ftp scheme rejected",
			input:   "ftp://gitlab.com/foo/bar/-/pipelines/1",
			wantErr: true,
		},
		{
			name:    "ssh scheme rejected",
			input:   "ssh://gitlab.com/foo/bar/-/pipelines/1",
			wantErr: true,
		},
		{
			name:    "file scheme rejected",
			input:   "file:///foo/bar/-/pipelines/1",
			wantErr: true,
		},
		{
			name:    "zero id rejected",
			input:   "https://gitlab.com/foo/bar/-/pipelines/0",
			wantErr: true,
		},
		{
			name:    "negative id rejected",
			input:   "https://gitlab.com/foo/bar/-/pipelines/-5",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path, id, err := ParsePipelineURL(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got path=%q id=%d", path, id)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if path != tc.wantPath {
				t.Errorf("path: got %q want %q", path, tc.wantPath)
			}
			if id != tc.wantPipeID {
				t.Errorf("id: got %d want %d", id, tc.wantPipeID)
			}
		})
	}
}

// stubService records which Service methods got called and returns
// caller-configured responses. The resolver-under-test should hit
// exactly the methods we expect for each input form; deviations indicate
// a regression in the resolution logic.
type stubService struct {
	Service           // embed so we only need to override what the resolver calls
	getProjectFn      func(ctx context.Context, idOrPath string) (ProjectNode, error)
	latestPipelineFn  func(ctx context.Context, projectID int, ref string) (PipelineSummary, error)
	latestForSHAFn    func(ctx context.Context, projectID int, sha string) (PipelineSummary, error)
	getPipelineCalled int
	getProjectArg     string
	latestPipelineRef string
	latestForSHAArg   string
}

func (s *stubService) GetProject(ctx context.Context, idOrPath string) (ProjectNode, error) {
	s.getProjectArg = idOrPath
	if s.getProjectFn != nil {
		return s.getProjectFn(ctx, idOrPath)
	}
	return ProjectNode{ID: 42, PathWithNamespace: idOrPath}, nil
}

func (s *stubService) LatestPipeline(ctx context.Context, projectID int, ref string) (PipelineSummary, error) {
	s.latestPipelineRef = ref
	if s.latestPipelineFn != nil {
		return s.latestPipelineFn(ctx, projectID, ref)
	}
	return PipelineSummary{ID: 7000}, nil
}

func (s *stubService) LatestPipelineForSHA(ctx context.Context, projectID int, sha string) (PipelineSummary, error) {
	s.latestForSHAArg = sha
	if s.latestForSHAFn != nil {
		return s.latestForSHAFn(ctx, projectID, sha)
	}
	return PipelineSummary{ID: 8000}, nil
}

func (s *stubService) GetPipeline(ctx context.Context, projectID, pipelineID int) (PipelineSummary, error) {
	s.getPipelineCalled++
	return PipelineSummary{ID: pipelineID}, nil
}

// TestParseJobURL exercises the job-URL parser shape. Same surface as
// ParsePipelineURL but the delimiter is "/-/jobs/"; passing a pipeline
// URL here must fail rather than silently misroute the user.
func TestParseJobURL(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantPath  string
		wantJobID int
		wantErr   bool
	}{
		{
			name:      "gitlab.com plain",
			input:     "https://gitlab.com/foo/bar/-/jobs/4567890",
			wantPath:  "foo/bar",
			wantJobID: 4567890,
		},
		{
			name:      "subgroup with trailing path",
			input:     "https://gitlab.mycompany.com/group/sub/project/-/jobs/100/raw",
			wantPath:  "group/sub/project",
			wantJobID: 100,
		},
		{
			name:      "with anchor",
			input:     "https://gitlab.com/foo/bar/-/jobs/9876#L42",
			wantPath:  "foo/bar",
			wantJobID: 9876,
		},
		{
			name:    "pipeline url rejected as job url",
			input:   "https://gitlab.com/foo/bar/-/pipelines/123",
			wantErr: true,
		},
		{
			name:    "non-numeric",
			input:   "https://gitlab.com/foo/bar/-/jobs/abc",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path, id, err := ParseJobURL(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got path=%q id=%d", path, id)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if path != tc.wantPath || id != tc.wantJobID {
				t.Errorf("got (%q, %d), want (%q, %d)", path, id, tc.wantPath, tc.wantJobID)
			}
		})
	}
}

// TestResolveJobRef_NumericRequiresProject locks in the contract: a
// bare numeric job ID can't be resolved without a project context, since
// the GitLab API endpoint is project-scoped even though job IDs are
// globally unique.
func TestResolveJobRef_NumericRequiresProject(t *testing.T) {
	s := &stubService{}
	_, err := ResolveJobRef(context.Background(), s, "12345", ResolveHints{})
	if !errors.Is(err, ErrNoProjectContext) {
		t.Fatalf("got %v, want ErrNoProjectContext", err)
	}
}

// TestResolveJobRef_URLOverridesProject: passing a job URL should
// resolve project from the URL, ignoring whatever --project says. Same
// rule as ParsePipelineURL — paste-and-go should not need flag tweaks.
func TestResolveJobRef_URLOverridesProject(t *testing.T) {
	s := &stubService{}
	spec, err := ResolveJobRef(context.Background(), s,
		"https://gitlab.com/foo/bar/-/jobs/777",
		ResolveHints{ProjectFlag: "ignored/by/url"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.getProjectArg != "foo/bar" {
		t.Errorf("GetProject called with %q, want foo/bar", s.getProjectArg)
	}
	if spec.JobID != 777 {
		t.Errorf("JobID: got %d want 777", spec.JobID)
	}
}

// TestResolveJobRef_NumericSkipsLookup mirrors the pipeline resolver's
// fast-path: numeric --project should not trigger a Projects.Get round
// trip on top of the job-id arg.
func TestResolveJobRef_NumericSkipsLookup(t *testing.T) {
	s := &stubService{}
	spec, err := ResolveJobRef(context.Background(), s, "555",
		ResolveHints{ProjectFlag: "1234"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.getProjectArg != "" {
		t.Errorf("GetProject should NOT be called for numeric project; got %q", s.getProjectArg)
	}
	if spec.ProjectID != 1234 || spec.JobID != 555 {
		t.Errorf("got (proj=%d, job=%d), want (1234, 555)", spec.ProjectID, spec.JobID)
	}
}

// TestResolveJobRef_BadForm: anything that's neither numeric nor a URL
// must fail with a "unrecognized" error so the user diagnostic isn't a
// confusing chain through unrelated 404s.
func TestResolveJobRef_BadForm(t *testing.T) {
	s := &stubService{}
	_, err := ResolveJobRef(context.Background(), s, "deploy-job",
		ResolveHints{ProjectFlag: "1234"})
	if err == nil {
		t.Fatal("expected error on bad form")
	}
	if !strings.Contains(err.Error(), "unrecognized") {
		t.Errorf("error should be 'unrecognized', got: %v", err)
	}
}

// TestResolvePipelineRef_URL: URL form should not consult --project at
// all, and should look up the project by its URL path component.
func TestResolvePipelineRef_URL(t *testing.T) {
	s := &stubService{}
	spec, err := ResolvePipelineRef(context.Background(), s,
		"https://gitlab.com/foo/bar/-/pipelines/555",
		ResolveHints{ProjectFlag: "ignored/by/url"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.getProjectArg != "foo/bar" {
		t.Errorf("GetProject called with %q, want foo/bar (URL should override --project)", s.getProjectArg)
	}
	if spec.PipelineID != 555 {
		t.Errorf("PipelineID: got %d want 555", spec.PipelineID)
	}
	if spec.ProjectID != 42 {
		t.Errorf("ProjectID: got %d want 42 (stub default)", spec.ProjectID)
	}
}

// TestResolvePipelineRef_Numeric: numeric pipeline ID should not require
// a Projects.Get round trip when --project is also numeric (saves 100ms
// per `pipeline status N`).
func TestResolvePipelineRef_NumericNoLookup(t *testing.T) {
	s := &stubService{}
	spec, err := ResolvePipelineRef(context.Background(), s, "12345",
		ResolveHints{ProjectFlag: "9999"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.getProjectArg != "" {
		t.Errorf("GetProject should NOT be called for numeric project, got call with %q", s.getProjectArg)
	}
	if spec.ProjectID != 9999 {
		t.Errorf("ProjectID: got %d want 9999", spec.ProjectID)
	}
	if spec.PipelineID != 12345 {
		t.Errorf("PipelineID: got %d want 12345", spec.PipelineID)
	}
}

// TestResolvePipelineRef_AtRef: @branch should call LatestPipeline with
// the branch as the ref filter.
func TestResolvePipelineRef_AtRef(t *testing.T) {
	s := &stubService{}
	_, err := ResolvePipelineRef(context.Background(), s, "@feat/auth",
		ResolveHints{ProjectFlag: "1234"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.latestPipelineRef != "feat/auth" {
		t.Errorf("LatestPipeline ref: got %q want feat/auth", s.latestPipelineRef)
	}
}

// TestResolvePipelineRef_HEADUsesSHA: HEAD with a git SHA should query
// LatestPipelineForSHA, not LatestPipeline. SHA is more specific than
// branch (commit can outlive its branch).
func TestResolvePipelineRef_HEADUsesSHA(t *testing.T) {
	s := &stubService{}
	_, err := ResolvePipelineRef(context.Background(), s, "HEAD",
		ResolveHints{
			ProjectFlag: "1234",
			GitSHA:      "abc1234def5678",
			GitBranch:   "main",
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.latestForSHAArg != "abc1234def5678" {
		t.Errorf("LatestPipelineForSHA arg: got %q want abc1234def5678", s.latestForSHAArg)
	}
	if s.latestPipelineRef != "" {
		t.Errorf("LatestPipeline should NOT be called when SHA is available, got ref %q", s.latestPipelineRef)
	}
}

// TestResolvePipelineRef_HEADFallsBackToBranch: when GitSHA is empty but
// GitBranch is set (unusual edge case), HEAD falls back to branch-based
// lookup so the command still works.
func TestResolvePipelineRef_HEADFallsBackToBranch(t *testing.T) {
	s := &stubService{}
	_, err := ResolvePipelineRef(context.Background(), s, "HEAD",
		ResolveHints{
			ProjectFlag: "1234",
			GitBranch:   "main",
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.latestPipelineRef != "main" {
		t.Errorf("LatestPipeline ref: got %q want main", s.latestPipelineRef)
	}
}

// TestResolvePipelineRef_EmptyArgRequiresGit: empty arg with no git
// context should error rather than expanding to "latest". Guessing wrong
// here would have users watching the wrong pipeline.
func TestResolvePipelineRef_EmptyArgRequiresGit(t *testing.T) {
	s := &stubService{}
	_, err := ResolvePipelineRef(context.Background(), s, "",
		ResolveHints{ProjectFlag: "1234"})
	if err == nil {
		t.Fatal("expected error when arg is empty and no git context, got nil")
	}
	if !strings.Contains(err.Error(), "git context") {
		t.Errorf("error should mention git context, got: %v", err)
	}
}

// TestResolvePipelineRef_NoProject: no --project and no git remote
// returns ErrNoProjectContext, which CLI callers match to print a
// targeted "pass --project or run inside a clone" message.
func TestResolvePipelineRef_NoProject(t *testing.T) {
	s := &stubService{}
	_, err := ResolvePipelineRef(context.Background(), s, "@main",
		ResolveHints{})
	if !errors.Is(err, ErrNoProjectContext) {
		t.Fatalf("got %v, want ErrNoProjectContext", err)
	}
}

// TestResolvePipelineRef_UnknownFormFailsFast: garbage input should
// return "unrecognized pipeline reference" before any project lookup
// runs, so the diagnostic points at the actual mistake (typo'd ref)
// instead of cascading into a 404 from the unrelated project resolver.
func TestResolvePipelineRef_UnknownFormFailsFast(t *testing.T) {
	s := &stubService{}
	_, err := ResolvePipelineRef(context.Background(), s, "deploy-pipeline-1",
		ResolveHints{GitProjectPath: "foo/bar"})
	if err == nil {
		t.Fatal("expected error on garbage ref")
	}
	if !strings.Contains(err.Error(), "unrecognized pipeline reference") {
		t.Errorf("error should be 'unrecognized pipeline reference', got: %v", err)
	}
	if s.getProjectArg != "" {
		t.Errorf("GetProject should NOT run for garbage refs; got call with %q", s.getProjectArg)
	}
}

// TestResolvePipelineRef_Latest: "latest" resolves to LatestPipeline
// with an empty ref filter, matching the existing TUI's "latest across
// all branches" semantic.
func TestResolvePipelineRef_Latest(t *testing.T) {
	s := &stubService{}
	_, err := ResolvePipelineRef(context.Background(), s, "latest",
		ResolveHints{ProjectFlag: "1234"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.latestPipelineRef != "" {
		t.Errorf("LatestPipeline ref: got %q want empty (all branches)", s.latestPipelineRef)
	}
}
