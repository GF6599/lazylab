package ui

import (
	"fmt"
	"hash/fnv"
	"path/filepath"
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"lazylab/internal/gitlab"
)

// Layout constants for UI calculations
const (
	// headerFooterLines is the number of lines reserved for header and footer
	headerFooterLines = 6

	// halfPageScrollFactor is the divisor for half-page scrolling
	halfPageScrollFactor = 2

	// statusBarLines is the number of lines reserved for status/help bar
	statusBarLines = 2

	// Project list view width percentages
	projectListWidthPct   = 45 // Percentage of width for project list in projects view
	projectDetailWidthPct = 55 // Percentage of width for project detail in projects view (100 - projectListWidthPct)

	// Explorer view width percentages
	explorerParentWidthPct  = 25 // Parent directory listing width
	explorerCurrentWidthPct = 45 // Current directory listing width
	explorerPreviewWidthPct = 30 // File preview width

	// Pipeline view width percentages
	pipelineListWidthPct   = 20 // Pipeline list width
	pipelineStagesWidthPct = 40 // Pipeline stages/jobs width
	pipelineLogWidthPct    = 40 // Pipeline log preview width

	// Tree view widths for navigation (when in nested directory)
	treeParentWidthPct  = 30 // Parent directory listing
	treeCurrentWidthPct = 25 // Current directory listing
	treePreviewWidthPct = 45 // Remaining for preview

	// Minimum widths to prevent layout collapse
	minParentWidth  = 6
	minCurrentWidth = 6
	minTreeParent   = 12
	minTreeCurrent  = 12
	minInnerWidth   = 20
)

// isLoading returns true if anything is currently loading that should animate the spinner.
func (m *Model) isLoading() bool {
	// Project list loading
	if m.loading || m.backgroundLoading {
		return true
	}

	// Mode-specific loading states
	switch m.mode {
	case modeExplorer:
		// Explorer tree or file loading
		if cur := m.currentDirState(); cur != nil && cur.loading {
			return true
		}
		if m.explorer.preview.loading {
			return true
		}
	case modePipelines:
		// Pipeline view loading states
		if m.pipelineView.loading {
			return true
		}
		if m.pipelineView.logPreview.loading {
			return true
		}
		// Check if any stages or jobs are loading for selected pipeline
		if pipeline := m.selectedPipeline(); pipeline != nil {
			if m.pipelineView.stageLoading[pipeline.ID] {
				return true
			}
			if m.pipelineView.jobsLoading[pipeline.ID] {
				return true
			}
		}
	}

	return false
}

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
}

func pipelineRefLabel(project gitlab.ProjectNode, state pipelineState) string {
	if strings.TrimSpace(state.ref) != "" {
		if state.ref == pipelineAllRefsRef {
			if state.hasInfo && strings.TrimSpace(state.info.Ref) != "" {
				return strings.TrimSpace(state.info.Ref)
			}
			return pipelineAllRefsLabel
		}
		return state.ref
	}
	if strings.TrimSpace(project.DefaultBranch) != "" {
		return strings.TrimSpace(project.DefaultBranch)
	}
	return "all refs"
}

func pipelineStatusStyle(status string) lipgloss.Style {
	switch strings.ToLower(status) {
	case "success":
		return pipelineSuccess
	case "failed":
		return pipelineFailed
	case "running":
		return pipelineRunning
	case "pending", "created", "waiting_for_resource", "scheduled":
		return pipelinePending
	case "canceled", "canceled?":
		return pipelineCanceled
	case "skipped":
		return pipelineSkipped
	default:
		return pipelineUnknown
	}
}

func pipelineStatusLabel(status string) string {
	label := strings.ToUpper(strings.TrimSpace(status))
	if label == "" {
		return "UNKNOWN"
	}
	return label
}

func pipelineStatusBadge(status string) string {
	return pipelineStatusBadgeWithWidth(status, 0)
}

func pipelineStatusBadgeWithWidth(status string, labelWidth int) string {
	label := pipelineStatusLabel(status)
	if labelWidth > 0 {
		pad := labelWidth - ansi.StringWidth(label)
		if pad > 0 {
			label += strings.Repeat(" ", pad)
		}
	}
	return pipelineStatusStyle(status).Render(fmt.Sprintf("[%s]", label))
}

func renderPipelineEntryLine(line string, selected, focused bool) string {
	if selected && focused {
		return explorerSelectedStyle.Render(line)
	}
	if selected {
		return explorerPathStyle.Render(line)
	}
	return explorerFileStyle.Render(line)
}

func latestJobForStage(jobs []gitlab.PipelineJob, stage string) *gitlab.PipelineJob {
	var selected *gitlab.PipelineJob
	for i := range jobs {
		job := &jobs[i]
		if job.Stage != stage {
			continue
		}
		if selected == nil || job.ID > selected.ID {
			selected = job
		}
	}
	return selected
}

func stageJobSummary(jobs []gitlab.PipelineJob, stage string) string {
	if len(jobs) == 0 {
		return ""
	}
	total := 0
	counts := map[string]int{}
	for _, job := range jobs {
		if job.Stage != stage {
			continue
		}
		total++
		status := strings.ToLower(job.Status)
		if status == "" {
			status = "unknown"
		}
		counts[status]++
	}
	if total == 0 {
		return ""
	}
	parts := make([]string, 0, 3)
	for _, status := range []string{"success", "failed", "running", "pending", "canceled", "skipped", "manual", "unknown"} {
		if count := counts[status]; count > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", count, status))
		}
		if len(parts) >= 2 {
			break
		}
	}
	if len(parts) == 0 {
		return fmt.Sprintf(" (%d jobs)", total)
	}
	return fmt.Sprintf(" (%d jobs: %s)", total, strings.Join(parts, ", "))
}

func (m *Model) pipelineLogJob() *gitlab.PipelineJob {
	if m.pipelineView.logJobID == 0 {
		return nil
	}
	pipeline := m.selectedPipeline()
	if pipeline == nil {
		return nil
	}
	jobs := m.pipelineView.jobsCache[pipeline.ID]
	for i := range jobs {
		if jobs[i].ID == m.pipelineView.logJobID {
			return &jobs[i]
		}
	}
	return nil
}

func wrapText(s string, width int) string {
	if width <= 0 {
		return s
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return s
	}
	var lines []string
	line := words[0]
	for _, word := range words[1:] {
		if len(line)+1+len(word) > width {
			lines = append(lines, line)
			line = word
		} else {
			line += " " + word
		}
	}
	lines = append(lines, line)
	return strings.Join(lines, "\n")
}

type previewHighlightEntry struct {
	content     string
	highlighted bool
}

var glamourRendererCache = struct {
	mu      sync.Mutex
	byWidth map[int]*glamour.TermRenderer
}{
	byWidth: make(map[int]*glamour.TermRenderer),
}

func (m *Model) highlightPreview(path, content string, width int) (string, bool, error) {
	if content == "" {
		return "", false, nil
	}
	if width <= 0 {
		width = 80
	}
	key := previewHighlightKey(path, width, content)
	if entry, ok := m.previewHighlightCache[key]; ok {
		return entry.content, entry.highlighted, nil
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
		m.previewHighlightCache = make(map[string]previewHighlightEntry)
		m.previewHighlightOrder = make([]string, 0, maxPreviewHighlightEntries)
	}
	if _, exists := m.previewHighlightCache[key]; exists {
		m.previewHighlightCache[key] = entry
		return
	}
	m.previewHighlightCache[key] = entry
	m.previewHighlightOrder = append(m.previewHighlightOrder, key)
	for len(m.previewHighlightOrder) > maxPreviewHighlightEntries {
		oldest := m.previewHighlightOrder[0]
		m.previewHighlightOrder = m.previewHighlightOrder[1:]
		delete(m.previewHighlightCache, oldest)
	}
}

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
		glamour.WithAutoStyle(),
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

func wrapPreviewLine(line string, width int) []string {
	if width <= 0 {
		return []string{line}
	}
	if line == "" {
		return []string{""}
	}
	var segments []string
	var b strings.Builder
	currentWidth := 0
	for _, r := range line {
		rw := lipgloss.Width(string(r))
		if currentWidth+rw > width && b.Len() > 0 {
			segments = append(segments, b.String())
			b.Reset()
			currentWidth = 0
		}
		if rw > width {
			segments = append(segments, string(r))
			continue
		}
		b.WriteRune(r)
		currentWidth += rw
		if currentWidth == width {
			segments = append(segments, b.String())
			b.Reset()
			currentWidth = 0
		}
	}
	if b.Len() > 0 {
		segments = append(segments, b.String())
	}
	if len(segments) == 0 {
		return []string{""}
	}
	return segments
}

func fuzzyMatch(target, pattern string) bool {
	targetRunes := []rune(strings.ToLower(target))
	patternRunes := []rune(strings.ToLower(pattern))
	if len(patternRunes) == 0 {
		return true
	}
	tIdx := 0
	for _, r := range patternRunes {
		found := false
		for tIdx < len(targetRunes) {
			if targetRunes[tIdx] == r {
				found = true
				tIdx++
				break
			}
			tIdx++
		}
		if !found {
			return false
		}
	}
	return true
}

func listPageStep(height int) int {
	visible := height - headerFooterLines
	if visible < 1 {
		visible = 1
	}
	step := visible / halfPageScrollFactor
	if step < 1 {
		step = 1
	}
	return step
}

func renderWithBottomHint(content, hint string, height int) string {
	if hint == "" {
		return content
	}
	return renderWithBottomLines(content, []string{hint}, height)
}

func renderWithBottomLines(content string, hints []string, height int) string {
	filtered := make([]string, 0, len(hints))
	for _, hint := range hints {
		if strings.TrimSpace(hint) != "" {
			filtered = append(filtered, hint)
		}
	}
	if len(filtered) == 0 {
		return content
	}
	if height <= 0 {
		trimmed := strings.TrimSuffix(content, "\n")
		lines := filtered
		if trimmed != "" {
			lines = append([]string{trimmed}, lines...)
		}
		return strings.Join(lines, "\n")
	}
	if height <= len(filtered) {
		return strings.Join(filtered[len(filtered)-height:], "\n")
	}
	trimmed := strings.TrimSuffix(content, "\n")
	var lines []string
	if trimmed != "" {
		lines = strings.Split(trimmed, "\n")
	}
	available := height - len(filtered)
	if len(lines) > available {
		lines = lines[:available]
	}
	for len(lines) < available {
		lines = append(lines, "")
	}
	lines = append(lines, filtered...)
	return strings.Join(lines, "\n")
}

func displayRef(ex explorerState) string {
	if ex.ref == "" {
		return "main"
	}
	return ex.ref
}

func parentDir(path string) string {
	if path == "" {
		return ""
	}
	path = strings.TrimSuffix(path, "/")
	idx := strings.LastIndex(path, "/")
	if idx == -1 {
		return ""
	}
	return path[:idx]
}

func (m *Model) findDirIndex(path string) int {
	for i := range m.explorer.stack {
		if m.explorer.stack[i].path == path {
			return i
		}
	}
	return -1
}

func normalizeColumn(content string, width, height int) []string {
	if width < 1 {
		width = 1
	}
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	result := make([]string, height)
	for i := 0; i < height; i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		result[i] = fitLine(line, width)
	}
	return result
}

func fitLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(line) > width {
		line = ansi.Truncate(line, width, "")
	}
	pad := width - ansi.StringWidth(line)
	if pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return line
}

func clampLine(line string, width int) string {
	if width <= 0 {
		return line
	}
	if lipgloss.Width(line) <= width {
		return line
	}
	if width == 1 {
		return "…"
	}
	var b strings.Builder
	currentWidth := 0
	for _, r := range line {
		rw := lipgloss.Width(string(r))
		if currentWidth+rw > width-1 {
			break
		}
		b.WriteRune(r)
		currentWidth += rw
	}
	return b.String() + "…"
}

func clampLines(text string, width int) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = clampLine(line, width)
	}
	return strings.Join(lines, "\n")
}

func clampLineANSI(line string, width int) string {
	if width <= 0 {
		return line
	}
	if ansi.StringWidth(line) <= width {
		return line
	}
	if width == 1 {
		return "…"
	}
	return ansi.Truncate(line, width, "…")
}

func overlayCentered(base, overlay string, width int) string {
	base = strings.TrimSuffix(base, "\n")
	overlay = strings.TrimSuffix(overlay, "\n")
	baseLines := strings.Split(base, "\n")
	if len(baseLines) == 0 {
		baseLines = []string{""}
	}
	if width <= 0 {
		for _, line := range baseLines {
			width = max(width, ansi.StringWidth(line))
		}
		if width == 0 {
			width = 1
		}
	}
	for i, line := range baseLines {
		baseLines[i] = padLineANSI(line, width)
	}
	overlayLines := strings.Split(overlay, "\n")
	if len(overlayLines) == 0 {
		return strings.Join(baseLines, "\n")
	}
	overlayWidth := 0
	for _, line := range overlayLines {
		overlayWidth = max(overlayWidth, ansi.StringWidth(line))
	}
	if overlayWidth == 0 {
		return strings.Join(baseLines, "\n")
	}
	overlayWidth = min(overlayWidth, width)
	overlayHeight := min(len(overlayLines), len(baseLines))
	x := max(0, (width-overlayWidth)/2)
	y := max(0, (len(baseLines)-overlayHeight)/2)
	for i := 0; i < overlayHeight && y+i < len(baseLines); i++ {
		line := padLineANSI(overlayLines[i], overlayWidth)
		end := min(width, x+overlayWidth)
		if end <= x {
			continue
		}
		baseLine := baseLines[y+i]
		left := ansi.Cut(baseLine, 0, x)
		right := ansi.Cut(baseLine, end, width)
		baseLines[y+i] = left + padLineANSI(line, end-x) + right
	}
	return strings.Join(baseLines, "\n")
}

func padLineANSI(line string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(line) > width {
		line = ansi.Truncate(line, width, "")
	}
	pad := width - ansi.StringWidth(line)
	if pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return line
}

func previewContentWidth(width int) int {
	if width <= 0 {
		width = 80
	}
	parentWidth := max(6, width*25/100)
	currentWidth := max(6, width*45/100)
	previewWidth := width - parentWidth - currentWidth
	if previewWidth < 6 {
		previewWidth = 6
		currentWidth = max(6, width-parentWidth-previewWidth)
	}
	contentWidth := previewWidth - 2
	if contentWidth < 1 {
		contentWidth = 1
	}
	return contentWidth
}

func previewContentHeight(height int) int {
	if height <= 5 {
		height = 5
	}
	return height - 2
}

func pipelineLogContentWidth(width int) int {
	if width <= 0 {
		width = 80
	}
	parentWidth := max(12, width*30/100)
	currentWidth := max(12, width*25/100)
	previewWidth := width - parentWidth - currentWidth
	if previewWidth < 12 {
		previewWidth = 12
		currentWidth = max(12, width-parentWidth-previewWidth)
	}
	contentWidth := previewWidth - 2
	if contentWidth < 1 {
		contentWidth = 1
	}
	return contentWidth
}

func pipelineLogContentHeight(height int) int {
	if height <= 5 {
		height = 5
	}
	return height - 2
}

func normalizePipelineLogContent(content string) string {
	if content == "" {
		return ""
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	if !strings.Contains(content, "\t") {
		return content
	}
	return strings.ReplaceAll(content, "\t", "    ")
}

func pipelineLogContentLines(preview previewState, width int) []string {
	if preview.content == "" {
		return nil
	}
	normalized := normalizePipelineLogContent(preview.content)
	wrapped := ansi.Wrap(normalized, width, "")
	return strings.Split(wrapped, "\n")
}

func previewContentLines(preview previewState, width int) []string {
	if preview.content == "" {
		return nil
	}
	lines := strings.Split(preview.content, "\n")
	if preview.highlighted {
		return lines
	}
	wrapped := make([]string, 0, len(lines))
	for _, line := range lines {
		segments := wrapPreviewLine(line, width)
		wrapped = append(wrapped, segments...)
	}
	return wrapped
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
	if err != nil && m.opts.Logger != nil {
		m.opts.Logger.Debug("rehighlight preview", "err", err)
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

// Pipeline log scrolling now handled by viewport directly in key handlers
