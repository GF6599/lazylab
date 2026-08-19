package gitlab

import (
	"context"
	"fmt"

	gl "gitlab.com/gitlab-org/api/client-go"
)

// MRService covers merge request listing, diffs, discussions, and creation —
// the full write surface needed by the review-style commands. Line-level
// commenting requires both GetMergeRequestDiffRefs (for the SHA triad) and
// CreateMergeRequestDiscussion (for the comment itself).
type MRService interface {
	ListMergeRequests(ctx context.Context, projectID int, opts MRListOptions) (MRPage, error)
	ListMergeRequestDiscussions(ctx context.Context, projectID, mrIID int) ([]MRDiscussion, error)
	ListMergeRequestDiffs(ctx context.Context, projectID, mrIID int) ([]MRDiffFile, error)
	ResolveMergeRequestDiscussion(ctx context.Context, projectID, mrIID int, discussionID string, resolved bool) error
	AddMergeRequestDiscussionNote(ctx context.Context, projectID, mrIID int, discussionID string, body string) error
	CreateMergeRequestDiscussion(ctx context.Context, projectID, mrIID int, body string, pos *MRCommentPosition) error
	GetMergeRequestDiffRefs(ctx context.Context, projectID, mrIID int) (MRDiffRefs, error)
	CreateMergeRequest(ctx context.Context, projectID int, opts CreateMROptions) (MergeRequestSummary, error)
}

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
			Page:    int64(opts.Page),
			PerPage: int64(opts.PerPage),
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
			IID:          int(mr.IID),
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
	meta := extractPageMeta(resp, opts.Page)
	return MRPage{
		MergeRequests: summaries,
		Page:          meta.Page,
		PrevPage:      meta.PrevPage,
		NextPage:      meta.NextPage,
		TotalPages:    meta.TotalPages,
		TotalItems:    meta.TotalItems,
	}, nil
}

// ListMergeRequestDiscussions returns all threaded discussions (across all
// pages) for a merge request. This includes both user comments and system
// notes. Callers that only want human-authored comments should filter out
// notes where System == true.
func (c *Client) ListMergeRequestDiscussions(ctx context.Context, projectID, mrIID int) ([]MRDiscussion, error) {
	raw, err := paginate(ctx, func(page int) ([]*gl.Discussion, *gl.Response, error) {
		opts := gl.ListMergeRequestDiscussionsOptions{
			ListOptions: gl.ListOptions{Page: int64(page), PerPage: 100},
		}
		return c.api.Discussions.ListMergeRequestDiscussions(projectID, int64(mrIID), &opts, gl.WithContext(ctx))
	})
	if err != nil {
		return nil, fmt.Errorf("list MR discussions: %w", err)
	}
	discussions := make([]MRDiscussion, 0, len(raw))
	for _, d := range raw {
		disc := MRDiscussion{ID: d.ID}
		for _, n := range d.Notes {
			note := MRNote{
				ID:         int(n.ID),
				Body:       n.Body,
				System:     n.System,
				Resolvable: n.Resolvable,
				Resolved:   n.Resolved,
			}
			// Author is a value struct in client-go; zero value (empty Name)
			// occurs for system notes or notes from deleted users.
			if n.Author.Name != "" {
				note.Author = n.Author.Name
			}
			if n.CreatedAt != nil {
				note.CreatedAt = *n.CreatedAt
			}
			// Prefer NewPath over OldPath: for renames, NewPath reflects the
			// file's current location; for edits both are identical.
			if n.Position != nil {
				note.OldLine = int(n.Position.OldLine)
				note.NewLine = int(n.Position.NewLine)
				if n.Position.NewPath != "" {
					note.FilePath = n.Position.NewPath
					note.Line = int(n.Position.NewLine)
				} else if n.Position.OldPath != "" {
					note.FilePath = n.Position.OldPath
					note.Line = int(n.Position.OldLine)
				}
				if note.Line == 0 && note.OldLine != 0 {
					note.Line = note.OldLine
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
	_, _, err := c.api.Discussions.ResolveMergeRequestDiscussion(projectID, int64(mrIID), discussionID, &opts, gl.WithContext(ctx))
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
	_, _, err := c.api.Discussions.AddMergeRequestDiscussionNote(projectID, int64(mrIID), discussionID, &opts, gl.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("add MR discussion note: %w", err)
	}
	return nil
}

// GetMergeRequestDiffRefs fetches the diff refs (base, head, start SHAs) for a
// merge request. These are needed to create line-level positioned comments.
//
// Returns an error if diff refs are unavailable, which can happen for MRs
// that haven't been prepared yet or MRs on projects with missing source branches.
func (c *Client) GetMergeRequestDiffRefs(ctx context.Context, projectID, mrIID int) (MRDiffRefs, error) {
	mr, _, err := c.api.MergeRequests.GetMergeRequest(projectID, int64(mrIID), nil, gl.WithContext(ctx))
	if err != nil {
		return MRDiffRefs{}, fmt.Errorf("get MR diff refs: %w", err)
	}
	if mr.DiffRefs.BaseSha == "" && mr.DiffRefs.HeadSha == "" && mr.DiffRefs.StartSha == "" {
		return MRDiffRefs{}, fmt.Errorf("get MR diff refs: diff refs not available")
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
			posOpts.OldLine = gl.Ptr(int64(pos.OldLine))
		}
		if pos.NewLine != 0 {
			posOpts.NewLine = gl.Ptr(int64(pos.NewLine))
		}
		opts.Position = posOpts
	}
	_, _, err := c.api.Discussions.CreateMergeRequestDiscussion(projectID, int64(mrIID), &opts, gl.WithContext(ctx))
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
			ListOptions: gl.ListOptions{Page: int64(page), PerPage: 100},
		}
		return c.api.MergeRequests.ListMergeRequestDiffs(projectID, int64(mrIID), opts, gl.WithContext(ctx))
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

// ListBranches returns branch names for a project, optionally filtered by a
// search string. When search is empty all branches are returned (up to 100).
// The result is a flat slice of branch names suitable for picker UIs.
func (c *Client) ListBranches(ctx context.Context, projectID int, search string) ([]string, error) {
	opts := &gl.ListBranchesOptions{
		ListOptions: gl.ListOptions{PerPage: 100},
	}
	if search != "" {
		opts.Search = gl.Ptr(search)
	}
	branches, _, err := c.api.Branches.ListBranches(projectID, opts, gl.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}
	names := make([]string, 0, len(branches))
	for _, b := range branches {
		names = append(names, b.Name)
	}
	return names, nil
}

// CreateMergeRequest creates a new merge request in the given project.
// Title, SourceBranch, and TargetBranch are required. The API always receives
// the target branch, and GitLab rejects an empty one with a 400, so an empty
// TargetBranch fails here with a message that names the field instead.
// Returns the created MR summary.
func (c *Client) CreateMergeRequest(ctx context.Context, projectID int, opts CreateMROptions) (MergeRequestSummary, error) {
	if opts.TargetBranch == "" {
		return MergeRequestSummary{}, fmt.Errorf("create merge request: target branch is required")
	}
	apiOpts := &gl.CreateMergeRequestOptions{
		Title:        gl.Ptr(opts.Title),
		SourceBranch: gl.Ptr(opts.SourceBranch),
		TargetBranch: gl.Ptr(opts.TargetBranch),
	}
	if opts.Description != "" {
		apiOpts.Description = gl.Ptr(opts.Description)
	}
	mr, _, err := c.api.MergeRequests.CreateMergeRequest(projectID, apiOpts, gl.WithContext(ctx))
	if err != nil {
		return MergeRequestSummary{}, fmt.Errorf("create merge request: %w", err)
	}
	s := MergeRequestSummary{
		IID:          int(mr.IID),
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
	return s, nil
}
