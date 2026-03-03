package gitlab

import (
	"context"
	"fmt"

	gl "gitlab.com/gitlab-org/api/client-go"
)

// ListMergeRequests returns a page of merge requests for a project.
func (c *Client) ListMergeRequests(ctx context.Context, projectID int, opts MRListOptions) (MRPage, error) {
	if opts.PerPage <= 0 {
		opts.PerPage = 25
	}
	if opts.Page <= 0 {
		opts.Page = 1
	}
	apiOpts := &gl.ListProjectMergeRequestsOptions{
		ListOptions: gl.ListOptions{
			Page:    opts.Page,
			PerPage: opts.PerPage,
		},
	}
	if opts.State != "" {
		apiOpts.State = gl.Ptr(opts.State)
	}
	mrs, resp, err := c.api.MergeRequests.ListProjectMergeRequests(projectID, apiOpts, gl.WithContext(ctx))
	if err != nil {
		return MRPage{}, fmt.Errorf("list merge requests: %w", err)
	}
	summaries := make([]MergeRequestSummary, 0, len(mrs))
	for _, mr := range mrs {
		s := MergeRequestSummary{
			IID:          mr.IID,
			Title:        mr.Title,
			State:        mr.State,
			SourceBranch: mr.SourceBranch,
			TargetBranch: mr.TargetBranch,
			WebURL:       mr.WebURL,
		}
		if mr.Author != nil {
			s.Author = mr.Author.Name
		}
		if mr.UpdatedAt != nil {
			s.UpdatedAt = *mr.UpdatedAt
		}
		summaries = append(summaries, s)
	}
	page := MRPage{
		MergeRequests: summaries,
		Page:          opts.Page,
	}
	if resp != nil {
		page.Page = resp.CurrentPage
		page.PrevPage = resp.PreviousPage
		page.NextPage = resp.NextPage
		page.TotalPages = resp.TotalPages
	}
	return page, nil
}
