package gitlab

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"sort"
	"time"

	gitlabapi "github.com/xanzy/go-gitlab"

	"gitlab-tui-codex/pkg/config"
	"gitlab-tui-codex/pkg/logging"
)

const requestTimeout = 15 * time.Second

// Client wraps the go-gitlab client with helper methods tailored to the TUI.
type Client struct {
	api *gitlabapi.Client
	cfg config.Config
}

// NewClient constructs a client configured with the provided settings.
func NewClient(cfg config.Config) (*Client, error) {
	client, err := gitlabapi.NewClient(cfg.Token, gitlabapi.WithBaseURL(cfg.Host))
	if err != nil {
		return nil, err
	}

	return &Client{
		api: client,
		cfg: cfg,
	}, nil
}

// ListProjects returns the most recent projects for the authenticated user.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	log := c.logger()
	start := time.Now()
	log.Debug("listing projects", "per_page", c.cfg.ProjectsPerPage)

	opts := &gitlabapi.ListProjectsOptions{
		Membership: gitlabapi.Bool(true),
		ListOptions: gitlabapi.ListOptions{
			PerPage: c.cfg.ProjectsPerPage,
			Page:    1,
		},
	}

	items, _, err := c.api.Projects.ListProjects(opts, gitlabapi.WithContext(ctx))
	if err != nil {
		log.Error("failed to list projects", "err", err)
		return nil, err
	}

	output := make([]Project, 0, len(items))
	for _, proj := range items {
		output = append(output, Project{
			ID:                proj.ID,
			Name:              proj.Name,
			PathWithNamespace: proj.PathWithNamespace,
			DefaultBranch:     proj.DefaultBranch,
			WebURL:            proj.WebURL,
			SSHURL:            proj.SSHURLToRepo,
			LastActivityAt:    derefTime(proj.LastActivityAt),
		})
	}

	sort.Slice(output, func(i, j int) bool {
		return output[i].LastActivityAt.After(output[j].LastActivityAt)
	})

	log.Info("projects retrieved", "count", len(output), "duration", time.Since(start))
	return output, nil
}

// ListPipelines fetches recent pipelines for a project.
func (c *Client) ListPipelines(ctx context.Context, projectID int) ([]Pipeline, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	log := c.logger().With("project_id", projectID)
	start := time.Now()
	log.Debug("listing pipelines")

	opts := &gitlabapi.ListProjectPipelinesOptions{
		OrderBy: gitlabapi.String("updated_at"),
		Sort:    gitlabapi.String("desc"),
		ListOptions: gitlabapi.ListOptions{
			PerPage: 25,
			Page:    1,
		},
	}

	pipes, _, err := c.api.Pipelines.ListProjectPipelines(projectID, opts, gitlabapi.WithContext(ctx))
	if err != nil {
		log.Error("failed to list pipelines", "err", err)
		return nil, err
	}

	result := make([]Pipeline, 0, len(pipes))
	for _, pipe := range pipes {
		result = append(result, Pipeline{
			ID:        pipe.ID,
			IID:       pipe.IID,
			Status:    pipe.Status,
			Ref:       pipe.Ref,
			WebURL:    pipe.WebURL,
			SHA:       pipe.SHA,
			UpdatedAt: derefTime(pipe.UpdatedAt),
		})
	}

	log.Debug("pipelines retrieved", "count", len(result), "duration", time.Since(start))
	return result, nil
}

// ListTree returns a repository tree for the given path.
func (c *Client) ListTree(ctx context.Context, projectID int, ref, path string) ([]TreeNode, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	log := c.logger().With("project_id", projectID, "ref", ref, "path", path)
	start := time.Now()
	log.Debug("listing repository tree")

	opts := &gitlabapi.ListTreeOptions{
		Ref: gitlabapi.String(ref),
		ListOptions: gitlabapi.ListOptions{
			PerPage: 200,
			Page:    1,
		},
	}
	if path != "" {
		opts.Path = gitlabapi.String(path)
	}

	nodes, _, err := c.api.Repositories.ListTree(projectID, opts, gitlabapi.WithContext(ctx))
	if err != nil {
		log.Error("failed to list repository tree", "err", err)
		return nil, err
	}

	result := make([]TreeNode, 0, len(nodes))
	for _, node := range nodes {
		result = append(result, TreeNode{
			Name: node.Name,
			Path: node.Path,
			Type: node.Type,
		})
	}

	log.Debug("repository tree retrieved", "count", len(result), "duration", time.Since(start))
	return result, nil
}

// GetFileContent fetches a single file blob for preview purposes.
func (c *Client) GetFileContent(ctx context.Context, projectID int, ref, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	log := c.logger().With("project_id", projectID, "ref", ref, "path", path)
	start := time.Now()
	log.Debug("fetching file content")

	file, _, err := c.api.RepositoryFiles.GetFile(projectID, path, &gitlabapi.GetFileOptions{
		Ref: gitlabapi.String(ref),
	}, gitlabapi.WithContext(ctx))
	if err != nil {
		log.Error("failed to fetch file", "err", err)
		return "", err
	}

	data, err := base64.StdEncoding.DecodeString(file.Content)
	if err != nil {
		log.Error("failed to decode file content", "err", err)
		return "", fmt.Errorf("decode file content: %w", err)
	}

	log.Debug("file content fetched", "size", len(data), "duration", time.Since(start))
	return string(data), nil
}

func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func (c *Client) logger() *slog.Logger {
	return logging.Logger().With("component", "gitlab_client")
}
