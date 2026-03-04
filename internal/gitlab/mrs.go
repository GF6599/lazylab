package gitlab

import (
	"context"
	"fmt"

	gl "gitlab.com/gitlab-org/api/client-go"
)

// ListMergeRequests returns a single page of merge requests for a project.
// Filter by state ("opened", "closed", "merged", "all") via opts.State; an
// empty State omits the filter and defaults to GitLab's server-side default
// (which is "all").
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

// ListMergeRequestDiscussions returns all threaded discussions (across all
// pages) for a merge request. This includes both user comments and system
// notes. Callers that only want human-authored comments should filter out
// notes where System == true.
func (c *Client) ListMergeRequestDiscussions(ctx context.Context, projectID, mrIID int) ([]MRDiscussion, error) {
	raw, err := paginate(ctx, func(page int) ([]*gl.Discussion, *gl.Response, error) {
		opts := gl.ListMergeRequestDiscussionsOptions{
			Page:    page,
			PerPage: 100,
		}
		return c.api.Discussions.ListMergeRequestDiscussions(projectID, mrIID, &opts, gl.WithContext(ctx))
	})
	if err != nil {
		return nil, fmt.Errorf("list MR discussions: %w", err)
	}
	discussions := make([]MRDiscussion, 0, len(raw))
	for _, d := range raw {
		disc := MRDiscussion{ID: d.ID}
		for _, n := range d.Notes {
			note := MRNote{
				ID:         n.ID,
				Body:       n.Body,
				System:     n.System,
				Resolvable: n.Resolvable,
				Resolved:   n.Resolved,
			}
			note.Author = n.Author.Name
			if n.CreatedAt != nil {
				note.CreatedAt = *n.CreatedAt
			}
			disc.Notes = append(disc.Notes, note)
		}
		discussions = append(discussions, disc)
	}
	return discussions, nil
}

// ResolveMergeRequestDiscussion toggles the resolved state of a discussion thread.
func (c *Client) ResolveMergeRequestDiscussion(ctx context.Context, projectID, mrIID int, discussionID string, resolved bool) error {
	opts := gl.ResolveMergeRequestDiscussionOptions{
		Resolved: gl.Ptr(resolved),
	}
	_, _, err := c.api.Discussions.ResolveMergeRequestDiscussion(projectID, mrIID, discussionID, &opts, gl.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("resolve MR discussion: %w", err)
	}
	return nil
}

// AddMergeRequestDiscussionNote posts a reply to an existing discussion thread.
func (c *Client) AddMergeRequestDiscussionNote(ctx context.Context, projectID, mrIID int, discussionID string, body string) error {
	opts := gl.AddMergeRequestDiscussionNoteOptions{
		Body: gl.Ptr(body),
	}
	_, _, err := c.api.Discussions.AddMergeRequestDiscussionNote(projectID, mrIID, discussionID, &opts, gl.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("add MR discussion note: %w", err)
	}
	return nil
}

// ListMergeRequestDiffs returns every changed file (across all pages) in a
// merge request. The Diff field contains the unified diff text; NewFile,
// RenamedFile, and DeletedFile flags indicate the change type.
func (c *Client) ListMergeRequestDiffs(ctx context.Context, projectID, mrIID int) ([]MRDiffFile, error) {
	raw, err := paginate(ctx, func(page int) ([]*gl.MergeRequestDiff, *gl.Response, error) {
		opts := &gl.ListMergeRequestDiffsOptions{
			ListOptions: gl.ListOptions{Page: page, PerPage: 100},
		}
		return c.api.MergeRequests.ListMergeRequestDiffs(projectID, mrIID, opts, gl.WithContext(ctx))
	})
	if err != nil {
		return nil, fmt.Errorf("list MR diffs: %w", err)
	}
	diffs := make([]MRDiffFile, 0, len(raw))
	for _, d := range raw {
		diffs = append(diffs, MRDiffFile{
			OldPath:     d.OldPath,
			NewPath:     d.NewPath,
			Diff:        d.Diff,
			NewFile:     d.NewFile,
			RenamedFile: d.RenamedFile,
			DeletedFile: d.DeletedFile,
		})
	}
	return diffs, nil
}
