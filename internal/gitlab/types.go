package gitlab

import "time"

// Project represents the fields that the TUI needs to render repositories.
type Project struct {
	ID                int
	Name              string
	PathWithNamespace string
	DefaultBranch     string
	WebURL            string
	SSHURL            string
	LastActivityAt    time.Time
}

// Pipeline captures a lightweight snapshot of project pipelines.
type Pipeline struct {
	ID        int
	IID       int
	Status    string
	Ref       string
	WebURL    string
	SHA       string
	UpdatedAt time.Time
}

// TreeNode mirrors the GitLab repository tree entry.
type TreeNode struct {
	Name string
	Path string
	Type string // "blob" or "tree"
}
