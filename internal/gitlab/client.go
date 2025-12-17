package gitlab

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"time"

	gl "github.com/xanzy/go-gitlab"
)

// Client is a small façade over go-gitlab that exposes higher-level types
// tailored for the TUI.
type Client struct {
	api  *gl.Client
	host string
}

// NewClient wires go-gitlab with the provided token and host.
func NewClient(token, host string) (*Client, error) {
	if token == "" {
		return nil, fmt.Errorf("gitlab token must not be empty")
	}
	trimmedHost := strings.TrimSpace(host)
	if trimmedHost == "" {
		trimmedHost = "https://gitlab.com"
	}
	baseURL := ensureAPIBaseURL(trimmedHost)
	api, err := gl.NewClient(token, gl.WithBaseURL(baseURL))
	if err != nil {
		return nil, fmt.Errorf("new client: %w", err)
	}
	return &Client{api: api, host: trimmedHost}, nil
}

// ProjectNode represents the subset of GitLab projects used by the UI.
type ProjectNode struct {
	ID                int
	Name              string
	PathWithNamespace string
	Description       string
	WebURL            string
	SSHURLToRepo      string
	LastActivityAt    time.Time
	StarCount         int
	Visibility        string
	DefaultBranch     string
}

// ProjectListOptions describe pagination parameters for project listings.
type ProjectListOptions struct {
	Page    int
	PerPage int
}

// ProjectPage contains a slice of projects along with pagination metadata.
type ProjectPage struct {
	Projects   []ProjectNode
	Page       int
	PrevPage   int
	NextPage   int
	TotalPages int
}

// TreeNode represents a repository tree entry (file or directory).
type TreeNode struct {
	Path string
	Name string
	Type string
	Mode string
}

// IsDir reports whether the node represents a directory.
func (n TreeNode) IsDir() bool {
	return n.Type == "tree"
}

// TreeListOptions configures repository tree listing.
type TreeListOptions struct {
	Path string
	Ref  string
}

// ListProjects returns the authenticated user's projects ordered by recent
// activity.
func (c *Client) ListProjects(ctx context.Context, opts ProjectListOptions) (ProjectPage, error) {
	if opts.PerPage <= 0 {
		opts.PerPage = 30
	}
	listOpts := &gl.ListProjectsOptions{
		Membership: gl.Bool(true),
		OrderBy:    gl.String("last_activity_at"),
		Sort:       gl.String("desc"),
		Simple:     gl.Bool(true),
		ListOptions: gl.ListOptions{
			Page:    opts.Page,
			PerPage: opts.PerPage,
		},
	}
	projects, resp, err := c.api.Projects.ListProjects(listOpts, gl.WithContext(ctx))
	if err != nil {
		return ProjectPage{}, err
	}
	nodes := make([]ProjectNode, len(projects))
	for i, p := range projects {
		nodes[i] = ProjectNode{
			ID:                p.ID,
			Name:              p.Name,
			PathWithNamespace: p.PathWithNamespace,
			Description:       p.Description,
			WebURL:            p.WebURL,
			SSHURLToRepo:      p.SSHURLToRepo,
			StarCount:         p.StarCount,
			Visibility:        string(p.Visibility),
			DefaultBranch:     p.DefaultBranch,
		}
		if p.LastActivityAt != nil {
			nodes[i].LastActivityAt = *p.LastActivityAt
		}
	}
	pageInfo := ProjectPage{
		Projects: nodes,
		Page:     opts.Page,
	}
	if resp != nil {
		pageInfo.Page = resp.CurrentPage
		pageInfo.PrevPage = resp.PreviousPage
		pageInfo.NextPage = resp.NextPage
		pageInfo.TotalPages = resp.TotalPages
	}
	return pageInfo, nil
}

// ListTree returns the immediate children of the path for the given project.
func (c *Client) ListTree(ctx context.Context, projectID int, opts TreeListOptions) ([]TreeNode, error) {
	treeOpts := &gl.ListTreeOptions{
		ListOptions: gl.ListOptions{
			PerPage: 200,
			Page:    1,
		},
		Ref:       gl.String(opts.Ref),
		Path:      gl.String(opts.Path),
		Recursive: gl.Bool(false),
	}
	nodes, _, err := c.api.Repositories.ListTree(projectID, treeOpts, gl.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("list tree: %w", err)
	}
	out := make([]TreeNode, len(nodes))
	for i, node := range nodes {
		out[i] = TreeNode{
			Path: node.Path,
			Name: node.Name,
			Type: node.Type,
			Mode: node.Mode,
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsDir() && !out[j].IsDir() {
			return true
		}
		if !out[i].IsDir() && out[j].IsDir() {
			return false
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// GetFileContent fetches the contents of a GitLab repository file at the given ref.
func (c *Client) GetFileContent(ctx context.Context, projectID int, path, ref string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("file path required")
	}
	file, _, err := c.api.RepositoryFiles.GetFile(projectID, path, &gl.GetFileOptions{
		Ref: gl.String(ref),
	}, gl.WithContext(ctx))
	if err != nil {
		return "", fmt.Errorf("get file: %w", err)
	}
	data, err := base64.StdEncoding.DecodeString(file.Content)
	if err != nil {
		return "", fmt.Errorf("decode file: %w", err)
	}
	return string(data), nil
}

func ensureAPIBaseURL(host string) string {
	host = strings.TrimSuffix(host, "/")
	if strings.HasSuffix(host, "/api/v4") {
		return host
	}
	return host + "/api/v4"
}
