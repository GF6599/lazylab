package ui

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"gitlab-tui-codex/internal/gitlab"
)

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

func pipelineStatusBadge(status string) string {
	label := strings.ToUpper(strings.TrimSpace(status))
	if label == "" {
		label = "UNKNOWN"
	}
	return pipelineStatusStyle(status).Render(fmt.Sprintf("[%s]", label))
}

func shortSHA(sha string) string {
	if len(sha) <= 8 {
		return sha
	}
	return sha[:8]
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

func highlightPreviewContent(path, content string, width int) (string, bool, error) {
	if content == "" {
		return "", false, nil
	}
	if width <= 0 {
		width = 80
	}
	if highlighted, err := highlightWithBat(path, content, width); err == nil {
		return highlighted, true, nil
	}
	if highlighted, err := highlightWithGlamour(path, content, width); err == nil {
		return highlighted, true, nil
	}
	return content, false, nil
}

func highlightWithBat(path, content string, width int) (string, error) {
	batPath, err := exec.LookPath("bat")
	if err != nil {
		batPath, err = exec.LookPath("batcat")
		if err != nil {
			return "", err
		}
	}
	args := []string{
		"--color=always",
		"--style=plain",
		"--paging=never",
		"--wrap=character",
		"--terminal-width",
		strconv.Itoa(width),
	}
	if path != "" {
		args = append(args, "--file-name", path)
	}
	cmd := exec.Command(batPath, args...)
	cmd.Stdin = strings.NewReader(content)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSuffix(out.String(), "\n"), nil
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
	renderer, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return "", err
	}
	out, err := renderer.Render(markdown)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(out, "\n"), nil
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func listPageStep(height int) int {
	visible := height - 6
	if visible < 1 {
		visible = 1
	}
	step := visible / 2
	if step < 1 {
		step = 1
	}
	return step
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
	if lipgloss.Width(line) > width {
		var b strings.Builder
		for _, r := range line {
			if lipgloss.Width(b.String()+string(r)) > width {
				break
			}
			b.WriteRune(r)
		}
		line = b.String()
	}
	pad := width - lipgloss.Width(line)
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

func previewContentWidth(width int) int {
	if width <= 0 {
		width = 80
	}
	parentWidth := max(6, width*20/100)
	currentWidth := max(6, width*40/100)
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
	parentWidth := max(12, width*20/100)
	currentWidth := max(12, width*40/100)
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

func (m *Model) scrollPreview(delta int) bool {
	preview := &m.explorer.preview
	if preview.raw == "" || preview.loading || preview.err != nil || preview.content == "" {
		return false
	}
	height := previewContentHeight(m.height)
	visibleHeight := max(0, height-1)
	if visibleHeight <= 0 {
		return false
	}
	width := previewContentWidth(m.width)
	contentLines := previewContentLines(*preview, width)
	maxOffset := max(0, len(contentLines)-visibleHeight)
	if maxOffset == 0 {
		preview.offset = 0
		return false
	}
	step := max(1, visibleHeight/2)
	next := preview.offset + (delta * step)
	if next < 0 {
		next = 0
	}
	if next > maxOffset {
		next = maxOffset
	}
	if next == preview.offset {
		return false
	}
	preview.offset = next
	return true
}

func (m *Model) scrollPreviewToStart() bool {
	preview := &m.explorer.preview
	if preview.raw == "" || preview.loading || preview.err != nil || preview.content == "" {
		return false
	}
	if preview.offset == 0 {
		return false
	}
	preview.offset = 0
	return true
}

func (m *Model) scrollPreviewToEnd() bool {
	preview := &m.explorer.preview
	if preview.raw == "" || preview.loading || preview.err != nil || preview.content == "" {
		return false
	}
	height := previewContentHeight(m.height)
	visibleHeight := max(0, height-1)
	if visibleHeight <= 0 {
		return false
	}
	width := previewContentWidth(m.width)
	contentLines := previewContentLines(*preview, width)
	maxOffset := max(0, len(contentLines)-visibleHeight)
	if preview.offset == maxOffset {
		return false
	}
	preview.offset = maxOffset
	return true
}

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
	highlighted, isHighlighted, err := highlightPreviewContent(preview.path, preview.raw, width)
	if err != nil && m.opts.Logger != nil {
		m.opts.Logger.Debug("rehighlight preview", "err", err)
		return
	}
	if isHighlighted {
		preview.content = highlighted
		preview.highlighted = true
		preview.highlightWidth = width
	}
	m.clampPreviewOffset()
}

func (m *Model) clampPreviewOffset() {
	preview := &m.explorer.preview
	if preview.raw == "" || preview.loading || preview.content == "" {
		preview.offset = 0
		return
	}
	height := previewContentHeight(m.height)
	visibleHeight := max(0, height-1)
	if visibleHeight <= 0 {
		preview.offset = 0
		return
	}
	width := previewContentWidth(m.width)
	contentLines := previewContentLines(*preview, width)
	maxOffset := max(0, len(contentLines)-visibleHeight)
	if preview.offset < 0 {
		preview.offset = 0
		return
	}
	if preview.offset > maxOffset {
		preview.offset = maxOffset
	}
}

func (m *Model) scrollPipelineLog(delta int) bool {
	if m.mode != modePipelines {
		return false
	}
	preview := &m.pipelineView.logPreview
	if preview.raw == "" && preview.content == "" {
		return false
	}
	if preview.loading || preview.err != nil {
		return false
	}
	height := pipelineLogContentHeight(m.height)
	visibleHeight := max(0, height-1)
	if visibleHeight <= 0 {
		return false
	}
	width := pipelineLogContentWidth(m.width)
	contentLines := pipelineLogContentLines(*preview, width)
	maxOffset := max(0, len(contentLines)-visibleHeight)
	if maxOffset == 0 {
		preview.offset = 0
		m.pipelineView.logAutoFollow = true
		return false
	}
	step := max(1, visibleHeight/2)
	next := preview.offset + (delta * step)
	if next < 0 {
		next = 0
	}
	if next > maxOffset {
		next = maxOffset
	}
	if next == preview.offset {
		return false
	}
	preview.offset = next
	if preview.offset == maxOffset {
		m.pipelineView.logAutoFollow = true
	} else if delta < 0 {
		m.pipelineView.logAutoFollow = false
	}
	return true
}

func (m *Model) scrollPipelineLogToStart() bool {
	if m.mode != modePipelines {
		return false
	}
	preview := &m.pipelineView.logPreview
	if preview.raw == "" && preview.content == "" {
		return false
	}
	if preview.loading || preview.err != nil {
		return false
	}
	if preview.offset == 0 {
		return false
	}
	preview.offset = 0
	m.pipelineView.logAutoFollow = false
	return true
}

func (m *Model) scrollPipelineLogToEnd() bool {
	if m.mode != modePipelines {
		return false
	}
	preview := &m.pipelineView.logPreview
	if preview.raw == "" && preview.content == "" {
		return false
	}
	if preview.loading || preview.err != nil {
		return false
	}
	height := pipelineLogContentHeight(m.height)
	visibleHeight := max(0, height-1)
	if visibleHeight <= 0 {
		return false
	}
	width := pipelineLogContentWidth(m.width)
	contentLines := pipelineLogContentLines(*preview, width)
	maxOffset := max(0, len(contentLines)-visibleHeight)
	if preview.offset == maxOffset {
		return false
	}
	preview.offset = maxOffset
	m.pipelineView.logAutoFollow = true
	return true
}

func (m *Model) clampPipelineLogOffset() {
	if m.mode != modePipelines {
		return
	}
	preview := &m.pipelineView.logPreview
	if preview.content == "" || preview.loading {
		preview.offset = 0
		return
	}
	height := pipelineLogContentHeight(m.height)
	visibleHeight := max(0, height-1)
	if visibleHeight <= 0 {
		preview.offset = 0
		return
	}
	width := pipelineLogContentWidth(m.width)
	contentLines := pipelineLogContentLines(*preview, width)
	maxOffset := max(0, len(contentLines)-visibleHeight)
	if preview.offset < 0 {
		preview.offset = 0
		return
	}
	if preview.offset > maxOffset {
		preview.offset = maxOffset
	}
	m.pipelineView.logAutoFollow = preview.offset == maxOffset
}

func (m *Model) tailPipelineLog() {
	if m.mode != modePipelines {
		return
	}
	preview := &m.pipelineView.logPreview
	if preview.content == "" || preview.loading {
		preview.offset = 0
		return
	}
	height := pipelineLogContentHeight(m.height)
	visibleHeight := max(0, height-1)
	if visibleHeight <= 0 {
		preview.offset = 0
		m.pipelineView.logAutoFollow = true
		return
	}
	width := pipelineLogContentWidth(m.width)
	contentLines := pipelineLogContentLines(*preview, width)
	maxOffset := max(0, len(contentLines)-visibleHeight)
	preview.offset = maxOffset
	m.pipelineView.logAutoFollow = true
}
