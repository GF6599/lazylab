// highlight.go implements syntax highlighting for file previews using glamour.
// Results are cached in an LRU keyed by path+width+content-hash to avoid
// re-rendering when re-selecting a previously viewed file.
package ui

import (
	"fmt"
	"hash/fnv"
	"path/filepath"
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
)

type previewHighlightEntry struct {
	content     string
	highlighted bool
}

// glamourRendererCache pools glamour.TermRenderer instances by terminal width.
// Glamour renderers are expensive to create (they compile markdown styles) but
// safe to reuse, so we cache one per distinct width. The cache is global and
// mutex-protected because terminal resize events can trigger concurrent access.
var glamourRendererCache = struct {
	mu      sync.Mutex
	byWidth map[int]*glamour.TermRenderer
}{
	byWidth: make(map[int]*glamour.TermRenderer),
}

// clearGlamourCache discards all cached glamour renderers so they are
// recreated on next use. Called by applyTheme to ensure renderer options
// stay in sync with the active theme.
func clearGlamourCache() {
	glamourRendererCache.mu.Lock()
	glamourRendererCache.byWidth = make(map[int]*glamour.TermRenderer)
	glamourRendererCache.mu.Unlock()
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
	highlighted, err := highlightWithGlamour(path, content, width)
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
func highlightWithGlamour(path, content string, width int) (string, error) {
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
	renderer, err := cachedGlamourRenderer(width)
	if err != nil {
		return "", err
	}
	out, err := renderer.Render(markdown)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(out, "\n"), nil
}

func cachedGlamourRenderer(width int) (*glamour.TermRenderer, error) {
	if width <= 0 {
		width = 80
	}
	glamourRendererCache.mu.Lock()
	renderer := glamourRendererCache.byWidth[width]
	glamourRendererCache.mu.Unlock()
	if renderer != nil {
		return renderer, nil
	}
	newRenderer, err := glamour.NewTermRenderer(
		glamour.WithStylePath("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil, err
	}
	glamourRendererCache.mu.Lock()
	if existing := glamourRendererCache.byWidth[width]; existing != nil {
		glamourRendererCache.mu.Unlock()
		return existing, nil
	}
	glamourRendererCache.byWidth[width] = newRenderer
	glamourRendererCache.mu.Unlock()
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
