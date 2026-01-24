package gitlab

import (
	"context"
	"strings"
	"testing"
)

func TestNewClient_EmptyToken(t *testing.T) {
	_, err := NewClient("", "https://gitlab.com")
	if err == nil {
		t.Fatal("Expected error for empty token")
	}
	if !strings.Contains(err.Error(), "token must not be empty") {
		t.Fatalf("Unexpected error message: %v", err)
	}
}

func TestNewClient_InvalidURL(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		wantErr string
	}{
		{
			name:    "malformed URL",
			host:    "ht!tp://invalid",
			wantErr: "invalid host URL",
		},
		{
			name:    "missing scheme",
			host:    "gitlab.com",
			wantErr: "host URL must use http or https scheme",
		},
		{
			name:    "ftp scheme",
			host:    "ftp://gitlab.com",
			wantErr: "host URL must use http or https scheme",
		},
		{
			name:    "file scheme",
			host:    "file:///etc/passwd",
			wantErr: "host URL must use http or https scheme",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewClient("valid-token", tt.host)
			if err == nil {
				t.Fatal("Expected error for invalid host URL")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Expected error containing %q, got: %v", tt.wantErr, err)
			}
		})
	}
}

func TestNewClient_ValidURLs(t *testing.T) {
	tests := []struct {
		name string
		host string
	}{
		{
			name: "default gitlab.com",
			host: "",
		},
		{
			name: "https gitlab.com",
			host: "https://gitlab.com",
		},
		{
			name: "http gitlab",
			host: "http://gitlab.example.com",
		},
		{
			name: "with port",
			host: "https://gitlab.com:8080",
		},
		{
			name: "with path",
			host: "https://gitlab.com/api/v4",
		},
		{
			name: "self-hosted",
			host: "https://git.mycompany.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient("valid-token", tt.host)
			if err != nil {
				t.Fatalf("Unexpected error for valid URL %q: %v", tt.host, err)
			}
			if client == nil {
				t.Fatal("Expected non-nil client")
			}
			if client.api == nil {
				t.Fatal("Expected non-nil API client")
			}
		})
	}
}

func TestNewClient_WhitespaceHandling(t *testing.T) {
	client, err := NewClient("valid-token", "  https://gitlab.com  ")
	if err != nil {
		t.Fatalf("Unexpected error for URL with whitespace: %v", err)
	}
	if client == nil {
		t.Fatal("Expected non-nil client")
	}
	if client.host != "https://gitlab.com" {
		t.Fatalf("Expected trimmed host, got: %s", client.host)
	}
}

func TestEnsureAPIBaseURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://gitlab.com", "https://gitlab.com/api/v4"},
		{"https://gitlab.com/", "https://gitlab.com/api/v4"},
		{"https://gitlab.com/api/v4", "https://gitlab.com/api/v4"},
		{"https://gitlab.com/api/v4/", "https://gitlab.com/api/v4"},
		{"http://git.local:8080", "http://git.local:8080/api/v4"},
	}

	for _, tt := range tests {
		got := ensureAPIBaseURL(tt.input)
		if got != tt.want {
			t.Errorf("ensureAPIBaseURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestMergeStageStatus(t *testing.T) {
	tests := []struct {
		name      string
		current   string
		candidate string
		want      string
	}{
		{"failed wins over success", "success", "failed", "failed"},
		{"failed wins over running", "running", "failed", "failed"},
		{"canceled wins over success", "success", "canceled", "canceled"},
		{"running wins over pending", "pending", "running", "running"},
		{"running wins over success", "success", "running", "running"},
		{"manual wins over success", "success", "manual", "manual"},
		{"success stays if no higher priority", "success", "skipped", "success"},
		{"first status when empty", "", "running", "running"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeStageStatus(tt.current, tt.candidate)
			if got != tt.want {
				t.Errorf("mergeStageStatus(%q, %q) = %q, want %q",
					tt.current, tt.candidate, got, tt.want)
			}
		})
	}
}

func TestNormalizeStageStatus(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"SUCCESS", "success"},
		{"  Failed  ", "failed"},
		{"Running", "running"},
		{"", "unknown"},
		{"  ", "unknown"},
	}

	for _, tt := range tests {
		got := normalizeStageStatus(tt.input)
		if got != tt.want {
			t.Errorf("normalizeStageStatus(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestRank(t *testing.T) {
	// Test that failed has lowest rank (highest priority)
	if rank("failed") >= rank("success") {
		t.Error("failed should have lower rank than success")
	}
	if rank("failed") >= rank("running") {
		t.Error("failed should have lower rank than running")
	}

	// Test that success has higher rank (lower priority) than running
	if rank("success") <= rank("running") {
		t.Error("success should have higher rank than running")
	}

	// Test unknown status gets default rank
	unknownRank := rank("unknown-status")
	if unknownRank != stageStatusPriority["unknown"] {
		t.Errorf("Unknown status should get default rank %d, got %d",
			stageStatusPriority["unknown"], unknownRank)
	}
}

func TestTreeNode_IsDir(t *testing.T) {
	tests := []struct {
		name string
		node TreeNode
		want bool
	}{
		{
			name: "directory",
			node: TreeNode{Type: "tree", Path: "src", Name: "src"},
			want: true,
		},
		{
			name: "file",
			node: TreeNode{Type: "blob", Path: "README.md", Name: "README.md"},
			want: false,
		},
		{
			name: "empty type",
			node: TreeNode{Type: "", Path: "unknown", Name: "unknown"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.node.IsDir()
			if got != tt.want {
				t.Errorf("TreeNode.IsDir() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestErrNoPipelines(t *testing.T) {
	// Verify ErrNoPipelines is defined and can be compared
	err := ErrNoPipelines
	if err == nil {
		t.Fatal("ErrNoPipelines should not be nil")
	}
	if err.Error() != "no pipelines found" {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestErrNoJobs(t *testing.T) {
	// Verify ErrNoJobs is defined and can be compared
	err := ErrNoJobs
	if err == nil {
		t.Fatal("ErrNoJobs should not be nil")
	}
	if err.Error() != "no jobs found" {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestGetFileContent_EmptyPath(t *testing.T) {
	// GetFileContent should return error for empty path
	client, err := NewClient("valid-token", "https://gitlab.com")
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Use background context for testing
	ctx := context.Background()
	_, err = client.GetFileContent(ctx, 123, "", "main")
	if err == nil {
		t.Fatal("Expected error for empty file path")
	}
	if !strings.Contains(err.Error(), "file path required") {
		t.Errorf("Expected 'file path required' error, got: %v", err)
	}
}

func TestGetFileContent_PathTraversalValidation(t *testing.T) {
	client, err := NewClient("valid-token", "https://gitlab.com")
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()
	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{
			name:    "parent directory traversal",
			path:    "../etc/passwd",
			wantErr: "path traversal not allowed",
		},
		{
			name:    "nested traversal",
			path:    "src/../../etc/passwd",
			wantErr: "path traversal not allowed",
		},
		{
			name:    "absolute path",
			path:    "/etc/passwd",
			wantErr: "path traversal not allowed",
		},
		{
			name:    "dotdot in middle",
			path:    "foo/../bar",
			wantErr: "path traversal not allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.GetFileContent(ctx, 123, tt.path, "main")
			if err == nil {
				t.Fatalf("Expected error for path %q", tt.path)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Expected error containing %q, got: %v", tt.wantErr, err)
			}
		})
	}
}

func TestRetryJob_ZeroJobID(t *testing.T) {
	// RetryJob should return error for zero jobID
	client, err := NewClient("valid-token", "https://gitlab.com")
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()
	_, err = client.RetryJob(ctx, 123, 0)
	if err == nil {
		t.Fatal("Expected error for zero job ID")
	}
	if !strings.Contains(err.Error(), "missing job id") {
		t.Errorf("Expected 'missing job id' error, got: %v", err)
	}
}

func TestPipelineSummary_Nil(t *testing.T) {
	// Test that nil pipeline returns empty summary
	got := pipelineSummary(nil)
	// Check that fields are zero values
	if got.ID != 0 || got.Status != "" || got.Ref != "" {
		t.Errorf("pipelineSummary(nil) should return zero PipelineSummary, got: %+v", got)
	}
	// Note: Full testing of pipelineSummary requires gitlab.com/gitlab-org/api/client-go types
	// which would require importing the full SDK just for testing. The function is simple
	// enough that coverage via integration tests is sufficient.
}

func TestPipelineStage_StatusPriority(t *testing.T) {
	// Test that status priorities work correctly in mergeStageStatus
	tests := []struct {
		name     string
		statuses []string
		want     string
	}{
		{
			name:     "failed wins",
			statuses: []string{"success", "running", "failed"},
			want:     "failed",
		},
		{
			name:     "canceled beats success",
			statuses: []string{"success", "canceled", "pending"},
			want:     "canceled",
		},
		{
			name:     "running beats success",
			statuses: []string{"success", "running", "skipped"},
			want:     "running",
		},
		{
			name:     "manual beats success",
			statuses: []string{"success", "manual", "skipped"},
			want:     "manual",
		},
		{
			name:     "success when all success",
			statuses: []string{"success", "success"},
			want:     "success",
		},
		{
			name:     "single status",
			statuses: []string{"pending"},
			want:     "pending",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ""
			for _, status := range tt.statuses {
				result = mergeStageStatus(result, status)
			}
			if result != tt.want {
				t.Errorf("mergeStageStatus(%v) = %q, want %q", tt.statuses, result, tt.want)
			}
		})
	}
}

// NOTE: Achieving >80% coverage for this package requires either:
// 1. API mocking infrastructure (httptest or dedicated mocking library)
// 2. Integration tests against a real GitLab instance (gated by GITLAB_TOKEN env)
// 3. Testdata fixtures with canned JSON responses
//
// Current tests focus on:
// - Input validation and error handling
// - Data transformation logic (helpers like mergeStageStatus)
// - Client initialization and configuration
//
// TODO: Add integration tests for:
// - ListProjects pagination behavior
// - ListTree sorting and filtering
// - LatestPipeline stage aggregation
// - RetryPipeline fallback logic
// - collectPipelineStages pagination

// TestGetFileContent_PathTraversal tests that path traversal attempts are blocked
func TestGetFileContent_PathTraversal(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantErr  string
	}{
		{
			name:    "simple dot-dot",
			path:    "../etc/passwd",
			wantErr: "invalid file path",
		},
		{
			name:    "encoded dot-dot",
			path:    "%2e%2e/etc/passwd",
			wantErr: "invalid file path",
		},
		{
			name:    "double encoded (passthrough)",
			path:    "%252e%252e/etc/passwd",
			wantErr: "", // Double-encoded passes validation but fails at API
		},
		{
			name:    "absolute path",
			path:    "/etc/passwd",
			wantErr: "invalid file path",
		},
		{
			name:    "mixed encoding",
			path:    "foo/%2e%2e/%2e%2e/etc/passwd",
			wantErr: "invalid file path",
		},
		{
			name:    "normalized to parent",
			path:    "foo/../../bar",
			wantErr: "invalid file path",
		},
	}

	// Note: These tests verify validation logic only.
	// They don't need a real GitLab API connection.
	client, err := NewClient("test-token", "https://gitlab.com")
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.GetFileContent(ctx, 12345, tt.path, "main")
			if err == nil {
				t.Fatalf("Expected error for path %q, got nil", tt.path)
			}
			if tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Expected error containing %q, got: %v", tt.wantErr, err)
			}
		})
	}
}

// TestGetFileContent_ValidPaths tests that legitimate paths are accepted
func TestGetFileContent_ValidPaths(t *testing.T) {
	tests := []string{
		"README.md",
		"src/main.go",
		"docs/api/v1/spec.yaml",
		"config.yml",
		".gitignore",
	}

	client, err := NewClient("test-token", "https://gitlab.com")
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()
	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			// This will fail with API error (no real API), but should pass validation
			_, err := client.GetFileContent(ctx, 12345, path, "main")
			if err != nil && strings.Contains(err.Error(), "invalid file path") {
				t.Errorf("Valid path %q rejected as traversal attempt: %v", path, err)
			}
		})
	}
}
