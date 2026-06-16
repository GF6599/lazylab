package gitlab

import (
	"context"
	"fmt"

	gl "gitlab.com/gitlab-org/api/client-go"
)

// UserService covers identity calls for the authenticated GitLab user.
type UserService interface {
	CurrentUser(ctx context.Context) (UserInfo, error)
}

// UserInfo is the flat representation of the authenticated GitLab user as
// returned by GET /user. The TUI does not consume this today; the CLI uses
// it for `lazylab whoami` to confirm a token is valid and identify which
// account it belongs to — especially useful when juggling multiple instances.
type UserInfo struct {
	ID        int
	Username  string
	Name      string
	Email     string
	State     string
	WebURL    string
	AvatarURL string
	Bio       string
	IsAdmin   bool
}

// CurrentUser returns the authenticated user identified by the configured
// token. Maps to GET /user; a 401 here is the canonical "token is invalid or
// expired" signal and surfaces as APIError so the CLI can exit with the
// auth-failed code.
func (c *Client) CurrentUser(ctx context.Context) (UserInfo, error) {
	u, _, err := c.api.Users.CurrentUser(gl.WithContext(ctx))
	if err != nil {
		return UserInfo{}, fmt.Errorf("current user: %w", err)
	}
	if u == nil {
		return UserInfo{}, fmt.Errorf("current user: empty response")
	}
	return UserInfo{
		ID:        int(u.ID),
		Username:  u.Username,
		Name:      u.Name,
		Email:     u.Email,
		State:     u.State,
		WebURL:    u.WebURL,
		AvatarURL: u.AvatarURL,
		Bio:       u.Bio,
		IsAdmin:   u.IsAdmin,
	}, nil
}
