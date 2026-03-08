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
	// Defensive defaults: zero-value PerPage/Page are replaced so callers don't
	// have to worry about uninitialized MRListOptions producing empty results.
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
			// Prefer NewPath over OldPath: for renames, NewPath reflects the
			// file's current location; for edits both are identical.
			if n.Position != nil {
				if n.Position.NewPath != "" {
					note.FilePath = n.Position.NewPath
					note.Line = n.Position.NewLine
				} else if n.Position.OldPath != "" {
					note.FilePath = n.Position.OldPath
					note.Line = n.Position.OldLine
				}
			}
			disc.Notes = append(disc.Notes, note)
		}
		discussions = append(discussions, disc)
	}
	return discussions, nil
}

// ResolveMergeRequestDiscussion toggles the resolved state of a discussion thread.
// Pass resolved=true to mark resolved, false to reopen. Only resolvable discussions
// (diff-line comments) can be toggled; calling on a non-resolvable discussion returns
// a GitLab API error.
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
// The body supports GitLab-flavored Markdown. The note is attributed to the user
// whose token authenticated the API client.
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

// GetMergeRequestDiffRefs fetches the diff refs (base, head, start SHAs) for a
// merge request. These are needed to create line-level positioned comments.
func (c *Client) GetMergeRequestDiffRefs(ctx context.Context, projectID, mrIID int) (MRDiffRefs, error) {
	mr, _, err := c.api.MergeRequests.GetMergeRequest(projectID, mrIID, nil, gl.WithContext(ctx))
	if err != nil {
		return MRDiffRefs{}, fmt.Errorf("get MR diff refs: %w", err)
	}
	return MRDiffRefs{
		BaseSHA:  mr.DiffRefs.BaseSha,
		HeadSHA:  mr.DiffRefs.HeadSha,
		StartSHA: mr.DiffRefs.StartSha,
	}, nil
}

// CreateMergeRequestDiscussion creates a new discussion on a merge request.
// When pos is nil, a general comment is created. When pos is non-nil, a
// line-level diff comment is created anchored to the specified file and line.
func (c *Client) CreateMergeRequestDiscussion(ctx context.Context, projectID, mrIID int, body string, pos *MRCommentPosition) error {
	opts := gl.CreateMergeRequestDiscussionOptions{
		Body: gl.Ptr(body),
	}
	if pos != nil {
		posOpts := &gl.PositionOptions{
			PositionType: gl.Ptr("text"),
			BaseSHA:      gl.Ptr(pos.DiffRefs.BaseSHA),
			HeadSHA:      gl.Ptr(pos.DiffRefs.HeadSHA),
			StartSHA:     gl.Ptr(pos.DiffRefs.StartSHA),
			OldPath:      gl.Ptr(pos.OldPath),
			NewPath:      gl.Ptr(pos.NewPath),
		}
		if pos.OldLine != 0 {
			posOpts.OldLine = gl.Ptr(pos.OldLine)
		}
		if pos.NewLine != 0 {
			posOpts.NewLine = gl.Ptr(pos.NewLine)
		}
		opts.Position = posOpts
	}
	_, _, err := c.api.Discussions.CreateMergeRequestDiscussion(projectID, mrIID, &opts, gl.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("create MR discussion: %w", err)
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
