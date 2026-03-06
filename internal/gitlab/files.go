package gitlab

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	gl "gitlab.com/gitlab-org/api/client-go"
)

// ListTree returns the immediate children of a directory in the repository,
// sorted directories-first then case-insensitively by name — matching the
// convention used by file managers like yazi and ranger. Results are fetched
// non-recursively with a single 200-item page, which is sufficient for most
// directories and avoids expensive recursive tree walks.
func (c *Client) ListTree(ctx context.Context, projectID int, opts TreeListOptions) ([]TreeNode, error) {
	treeOpts := &gl.ListTreeOptions{
		ListOptions: gl.ListOptions{
			PerPage: 200,
			Page:    1,
		},
		Ref:       gl.Ptr(opts.Ref),
		Path:      gl.Ptr(opts.Path),
		Recursive: gl.Ptr(false),
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
	slices.SortStableFunc(out, func(a, b TreeNode) int {
		if a.IsDir() && !b.IsDir() {
			return -1
		}
		if !a.IsDir() && b.IsDir() {
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out, nil
}

// GetFileContent fetches and decodes a single file from the repository.
//
// Security: the path is validated against directory traversal attacks (e.g.,
// "../../etc/passwd") using both raw string checks and filepath.Clean
// normalisation after URL-decoding, to guard against percent-encoded bypasses.
//
// Size: files larger than 10 MB are rejected before decoding to prevent
// excessive memory allocation in the TUI process. The GitLab API returns file
// content base64-encoded, so the check happens on the server-reported size
// before the decode step.
func (c *Client) GetFileContent(ctx context.Context, projectID int, path, ref string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("file path required")
	}

	// Validate path for traversal attempts
	if strings.Contains(path, "..") || strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("invalid file path: path traversal not allowed")
	}

	// Additional security: decode URL encoding and check for unicode tricks
	decodedPath, err := url.PathUnescape(path)
	if err != nil {
		return "", fmt.Errorf("invalid file path encoding: %w", err)
	}

	// Normalize and verify path stays within bounds
	cleanPath := filepath.Clean(decodedPath)
	if strings.Contains(cleanPath, "..") || filepath.IsAbs(cleanPath) {
		return "", fmt.Errorf("invalid file path: normalization detected traversal attempt")
	}

	file, _, err := c.api.RepositoryFiles.GetFile(projectID, path, &gl.GetFileOptions{
		Ref: gl.Ptr(ref),
	}, gl.WithContext(ctx))
	if err != nil {
		return "", fmt.Errorf("get file: %w", err)
	}

	// Check file size before decoding (size is in bytes)
	const maxFileSize = 10 * 1024 * 1024 // 10 MB
	if file.Size > maxFileSize {
		return "", fmt.Errorf("file too large: %d bytes (max %d bytes)", file.Size, maxFileSize)
	}

	data, err := base64.StdEncoding.DecodeString(file.Content)
	if err != nil {
		return "", fmt.Errorf("decode file: %w", err)
	}
	return string(data), nil
}
