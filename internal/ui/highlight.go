// highlight.go implements syntax highlighting for file previews using glamour.
// Results are cached in an LRU keyed by path+width+content-hash to avoid
// re-rendering when re-selecting a previously viewed file.
package ui

import (
	"fmt"
	"hash/fnv"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/glamour"
)

type previewHighlightEntry struct {
	content     string
	highlighted bool
}

// highlightPreview applies glamour syntax highlighting to file content and
// caches the result. The cache is a bounded LRU (maxPreviewHighlightEntries)
// keyed by path+width+content-hash, so re-selecting an already-viewed file
// avoids re-rendering. Entries larger than maxPreviewHighlightBytes are not
// cached to avoid memory pressure from large files.
func (m *Model) highlightPreview(path, content string, width int) (string, bool, error) {
	if content == "" {
		return "", false, nil
	}
	// Skip glamour entirely for oversized content. Pathological markdown can
	// make Render run unboundedly, and the result wouldn't fit in our LRU
	// anyway (line 67 below). Better to surface raw content immediately than
	// risk hanging the preview pane on an attacker-controlled file.
	if len(content) > maxPreviewHighlightBytes {
		return content, false, nil
	}
	if width <= 0 {
		width = 80
	}
	key := previewHighlightKey(path, width, content)
	if m.previewHighlightCache != nil {
		if entry, ok := m.previewHighlightCache.Get(key); ok {
			return entry.content, entry.highlighted, nil
		}
	}
	highlighted, err := m.highlightWithGlamour(path, content, width)
	if err != nil {
		return "", false, err
	}
	if highlighted == "" {
		return content, false, nil
	}
	entry := previewHighlightEntry{content: highlighted, highlighted: true}
	if len(entry.content) <= maxPreviewHighlightBytes {
		m.storePreviewHighlight(key, entry)
	}
	return highlighted, true, nil
}

func previewHighlightKey(path string, width int, content string) string {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(content))
	return fmt.Sprintf("%s:%d:%x", path, width, hasher.Sum64())
}

func (m *Model) storePreviewHighlight(key string, entry previewHighlightEntry) {
	if m.previewHighlightCache == nil {
		c := NewLRUCache[string, previewHighlightEntry](maxPreviewHighlightEntries)
		m.previewHighlightCache = &c
	}
	m.previewHighlightCache.Set(key, entry)
}

// highlightWithGlamour wraps content in a fenced code block with language
// detection and renders it through glamour. The fence delimiter is extended
// if the content itself contains triple-backticks to avoid parsing ambiguity.
func (m *Model) highlightWithGlamour(path, content string, width int) (string, error) {
	lang := languageFromPath(path)
	fence := "```"
	for strings.Contains(content, fence) {
		fence += "`"
	}
	header := fence
	if lang != "" {
		header += lang
	}
	markdown := header + "\n" + content + "\n" + fence + "\n"
	renderer, err := m.cachedGlamourRenderer(width)
	if err != nil {
		return "", err
	}
	out, err := renderer.Render(markdown)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(out, "\n"), nil
}

// cachedGlamourRenderer reuses a glamour.TermRenderer per terminal width.
// Renderers are expensive to construct (they compile markdown styles) but
// safe to reuse within a single Bubble Tea program — Update runs sequentially
// so no mutex is needed. The cache lives on Model so test isolation is
// preserved and theme changes can drop it via clearGlamourRenderers.
func (m *Model) cachedGlamourRenderer(width int) (*glamour.TermRenderer, error) {
	if width <= 0 {
		width = 80
	}
	if m.glamourRenderers == nil {
		m.glamourRenderers = make(map[int]*glamour.TermRenderer)
	}
	if renderer, ok := m.glamourRenderers[width]; ok {
		return renderer, nil
	}
	newRenderer, err := glamour.NewTermRenderer(
		glamour.WithStylePath("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil, err
	}
	m.glamourRenderers[width] = newRenderer
	return newRenderer, nil
}

func languageFromPath(path string) string {
	base := filepath.Base(path)
	switch base {
	case "Dockerfile":
		return "dockerfile"
	case "Makefile":
		return "makefile"
	}
	ext := strings.TrimPrefix(filepath.Ext(base), ".")
	return ext
}

// refreshPreviewHighlight re-renders syntax highlighting when terminal width changes
func (m *Model) refreshPreviewHighlight() {
	if m.mode != modeExplorer {
		return
	}
	preview := &m.explorer.preview
	if preview.raw == "" || preview.loading || !preview.highlighted {
		return
	}
	width := previewContentWidth(m.width)
	if preview.highlightWidth == width {
		return
	}
	highlighted, isHighlighted, err := m.highlightPreview(preview.path, preview.raw, width)
	if err != nil {
		m.logDebug("rehighlight preview", "err", err)
		return
	}
	if isHighlighted {
		preview.content = highlighted
		preview.highlighted = true
		preview.highlightWidth = width
		preview.viewport.SetContent(highlighted)
		return
	}
	preview.content = preview.raw
	preview.highlighted = false
	preview.highlightWidth = 0
	preview.viewport.SetContent(preview.raw)
}
