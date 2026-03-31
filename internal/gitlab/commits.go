package gitlab

import (
	"context"
	"fmt"

	gl "gitlab.com/gitlab-org/api/client-go"
)

// ListProjectCommits returns the most recent commits for a project, ordered
// newest first (server default). If ref is empty, GitLab uses the project's
// default branch. Limit defaults to 5 if zero or negative — this is enough
// for the detail pane preview without fetching excessive data.
func (c *Client) ListProjectCommits(ctx context.Context, projectID int, ref string, limit int) ([]CommitSummary, error) {
	if limit <= 0 {
		limit = 5
	}
	opts := &gl.ListCommitsOptions{
		ListOptions: gl.ListOptions{
			PerPage: int64(limit),
			Page:    1,
		},
	}
	if ref != "" {
		opts.RefName = gl.Ptr(ref)
	}
	commits, _, err := c.api.Commits.ListCommits(projectID, opts, gl.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("list commits: %w", err)
	}
	summaries := make([]CommitSummary, 0, len(commits))
	for _, c := range commits {
		s := CommitSummary{
			ShortID: c.ShortID,
			Title:   c.Title,
		}
		if c.AuthorName != "" {
			s.Author = c.AuthorName
		}
		if c.CreatedAt != nil {
			s.CreatedAt = *c.CreatedAt
		}
		summaries = append(summaries, s)
	}
	return summaries, nil
}
