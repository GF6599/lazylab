package gitlab

import (
	"context"
	"fmt"

	gl "gitlab.com/gitlab-org/api/client-go"
)

// ProjectService exposes project-level lookups: paginated listing for the
// browser and single-record resolution for the CLI's --project flag.
type ProjectService interface {
	ListProjects(ctx context.Context, opts ProjectListOptions) (ProjectPage, error)
	// GetProject resolves a project by numeric ID (passed as a string) or
	// by namespace path ("group/subgroup/project"). The GitLab API accepts
	// both forms via the same endpoint, so the resolver can hand its
	// --project arg through verbatim.
	GetProject(ctx context.Context, idOrPath string) (ProjectNode, error)
}

// GetProject fetches a single project by numeric ID (as a string) or by
// namespace path ("group/subgroup/project"). GitLab's API accepts both
// forms at the same endpoint — the SDK URL-encodes string paths
// automatically — so callers can pass --project values through without
// branching on the input shape.
//
// The returned ProjectNode is the same flat representation used elsewhere
// in this package; ListProjects's per-record mapping is intentionally
// duplicated here rather than factored out because GetProject can carry
// additional fields in the future (e.g. ContainerRegistry, MergeRequestsEnabled)
// without disturbing the list path's response size.
func (c *Client) GetProject(ctx context.Context, idOrPath string) (ProjectNode, error) {
	if idOrPath == "" {
		return ProjectNode{}, fmt.Errorf("get project: empty id or path")
	}
	p, _, err := c.api.Projects.GetProject(idOrPath, nil, gl.WithContext(ctx))
	if err != nil {
		return ProjectNode{}, fmt.Errorf("get project %q: %w", idOrPath, err)
	}
	if p == nil {
		return ProjectNode{}, fmt.Errorf("get project %q: empty response", idOrPath)
	}
	node := ProjectNode{
		ID:                int(p.ID),
		Name:              p.Name,
		PathWithNamespace: p.PathWithNamespace,
		Description:       p.Description,
		WebURL:            p.WebURL,
		SSHURLToRepo:      p.SSHURLToRepo,
		StarCount:         int(p.StarCount),
		Visibility:        string(p.Visibility),
		DefaultBranch:     p.DefaultBranch,
	}
	if p.LastActivityAt != nil {
		node.LastActivityAt = *p.LastActivityAt
	}
	return node, nil
}
