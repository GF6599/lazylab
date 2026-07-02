package gitlab

import (
	"context"
)

// ProjectService exposes paginated project listing for the browser.
type ProjectService interface {
	ListProjects(ctx context.Context, opts ProjectListOptions) (ProjectPage, error)
}
