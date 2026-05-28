// Package cliout renders subcommand output for the lazylab CLI. The TUI has
// its own lipgloss-driven rendering; cliout exists so non-interactive
// invocations can produce predictable, pipe-friendly text on stdout.
//
// The package deliberately keeps the surface tiny: a Format enum chosen via
// --format, plus a writer per format. New formats and richer renderers
// (tables, TSV) should land here as list-style subcommands need them, not
// preemptively.
package cliout

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
)

// HumanizeTime renders t as a relative duration string suitable for
// list tables ("2m ago", "1h ago", "3d ago"). Falls back to an ISO date
// for events older than a week — relative units stop being useful at
// month/year scales, and "47d ago" is harder to skim than "2026-04-08".
//
// A zero time produces "—" so empty rows don't render as bizarre "X ago"
// strings; this matches the convention used elsewhere in the TUI.
func HumanizeTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < 0:
		// Future timestamps (clock skew between machine and GitLab)
		// — show absolute time rather than "-3m ago" which is jarring.
		return t.UTC().Format("2006-01-02 15:04Z")
	case d < time.Minute:
		return "<1m ago"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.UTC().Format("2006-01-02")
	}
}

// Format selects how a subcommand renders its result. The zero value is
// FormatTable so callers that forget to populate the field still get
// human-readable output instead of an error.
type Format int

const (
	// FormatTable produces a human-readable rendering. For single-record
	// results this is an aligned key:value block; for lists (future) it
	// will be a column-aligned table.
	FormatTable Format = iota
	// FormatJSON produces indented JSON suitable for piping into jq.
	FormatJSON
)

// ParseFormat resolves a --format flag value. Empty string yields the
// default (FormatTable) so an unset flag does not have to be special-cased
// at the call site.
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "table", "text":
		return FormatTable, nil
	case "json":
		return FormatJSON, nil
	default:
		return FormatTable, fmt.Errorf("unknown format %q: expected one of table, json", s)
	}
}

// KV is a single label/value row in a key:value block. Empty Value rows are
// rendered as the label with a trailing colon, which is useful for visual
// section breaks (e.g. a heading row before its data).
type KV struct {
	Key   string
	Value string
}

// PrintJSON writes v as indented JSON followed by a newline. The two-space
// indent matches what `jq` produces by default, so eyeballed output looks
// identical to `lazylab ... --format json | jq`.
//
// Encoding goes through an intermediate buffer so a marshal failure
// midway through a complex value cannot leak a partial document onto w.
// That matters when callers stream multiple records (NDJSON-style):
// a single bad value would otherwise corrupt the entire stream and
// downstream `jq` would refuse the whole batch instead of skipping
// the failing record.
func PrintJSON(w io.Writer, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	if _, err := w.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write json: %w", err)
	}
	return nil
}

// Table is a minimal aligned-column writer for list-style output.
// Columns are sized to the widest header-or-cell value; rows are space-
// separated with no leading/trailing whitespace per line so the output
// pipes cleanly into awk, grep, and friends.
//
// No borders, no colors, no row separators — deliberately. Anything
// fancier and the output stops being a stable contract for scripts;
// stick to "human can read it, awk can parse it."
type Table struct {
	headers []string
	rows    [][]string
}

// NewTable creates a Table seeded with the given header row.
func NewTable(headers ...string) *Table {
	return &Table{headers: headers}
}

// AddRow appends a row. Cells exceeding len(headers) are dropped; cells
// shorter than len(headers) are padded with empty strings so the row is
// still well-formed.
func (t *Table) AddRow(cells ...string) {
	if len(cells) > len(t.headers) {
		cells = cells[:len(t.headers)]
	}
	for len(cells) < len(t.headers) {
		cells = append(cells, "")
	}
	t.rows = append(t.rows, cells)
}

// Render writes the table to w. Returns nil for an empty header set so
// callers don't have to special-case "no data" — useful for `--format
// table` paths where an empty list is a legitimate (silent) result.
//
// Column widths are measured in terminal display cells via go-runewidth
// rather than bytes or runes. That keeps Unicode names ("François"),
// CJK strings ("日本語", width 2 each), and emoji aligned with their
// ASCII neighbours. Falling back to len() would push everything after a
// multibyte cell into the next column.
func (t *Table) Render(w io.Writer) error {
	if len(t.headers) == 0 {
		return nil
	}
	widths := make([]int, len(t.headers))
	for i, h := range t.headers {
		widths[i] = runewidth.StringWidth(h)
	}
	for _, row := range t.rows {
		for i, cell := range row {
			if i >= len(widths) {
				break
			}
			if n := runewidth.StringWidth(cell); n > widths[i] {
				widths[i] = n
			}
		}
	}
	if err := writeTableRow(w, t.headers, widths); err != nil {
		return err
	}
	for _, row := range t.rows {
		// Belt-and-braces against rows constructed outside AddRow
		// (e.g., directly assigning t.rows in a test). Padding here
		// keeps Render total even if the invariant is broken upstream.
		if len(row) < len(widths) {
			padded := make([]string, len(widths))
			copy(padded, row)
			row = padded
		}
		if err := writeTableRow(w, row, widths); err != nil {
			return err
		}
	}
	return nil
}

// writeTableRow renders one row to w with two-space inter-column
// separation and no trailing whitespace on the final column. The row is
// always rendered across len(widths) columns: missing trailing cells are
// emitted as empty (and therefore padded) strings so a short row still
// terminates the line correctly instead of breaking column alignment.
func writeTableRow(w io.Writer, cells []string, widths []int) error {
	last := len(widths) - 1
	for i := 0; i < len(widths); i++ {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		if i == last {
			if _, err := fmt.Fprintf(w, "%s\n", cell); err != nil {
				return err
			}
			return nil
		}
		// Pad in display cells, not bytes: runewidth.FillRight appends
		// spaces until the visible width matches widths[i]. fmt's
		// %-*s pads by byte length, which under-pads multi-byte cells.
		if _, err := fmt.Fprintf(w, "%s  ", runewidth.FillRight(cell, widths[i])); err != nil {
			return err
		}
	}
	return nil
}

// PrintKV writes an aligned key:value block. Keys are right-padded to the
// longest key in the slice so values line up; rows with empty values are
// emitted as label-only to support section headings. The output is
// deliberately plain (no color, no box drawing) so it remains readable when
// redirected to a file or piped into another tool.
func PrintKV(w io.Writer, rows []KV) error {
	width := 0
	for _, r := range rows {
		// +1 accounts for the trailing colon appended below, so values
		// align to one column past the longest "key:" — not one column
		// past the longest bare key.
		if n := len(r.Key) + 1; n > width {
			width = n
		}
	}
	for _, r := range rows {
		if r.Value == "" {
			if _, err := fmt.Fprintf(w, "%s:\n", r.Key); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(w, "%-*s  %s\n", width, r.Key+":", r.Value); err != nil {
			return err
		}
	}
	return nil
}
