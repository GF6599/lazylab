package gitlab

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	gl "gitlab.com/gitlab-org/api/client-go"
)

// ListTree returns the immediate children of the path for the given project.
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
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsDir() && !out[j].IsDir() {
			return true
		}
		if !out[i].IsDir() && out[j].IsDir() {
			return false
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// GetFileContent fetches the contents of a GitLab repository file at the given ref.
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
