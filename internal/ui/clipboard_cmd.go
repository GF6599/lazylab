// clipboard_cmd.go isolates the single async path for clipboard writes.
//
// Why a Bubble Tea command instead of an inline call: clipboard.WriteAll can
// block for hundreds of milliseconds on Linux (Wayland/X11 selection-owner
// negotiation) and over SSH (OSC52 handshake). A synchronous call inside an
// Update branch freezes the event loop, dropping key events and animation
// frames. Wrapping the write in a tea.Cmd moves it onto Bubble Tea's worker
// goroutine pool and surfaces the result as a normal message.
//
// This is also the single point where the OS clipboard is written (via the
// clipboardWrite seam below); all copy methods route through writeClipboardCmd.

package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/atotto/clipboard"
)

// clipboardWrite is swapped by tests because CI machines have no OS clipboard
// and the copied text is the observable under test.
var clipboardWrite = clipboard.WriteAll

// clipboardWroteMsg is the result of an async clipboard write. status is the
// human-readable string that should land in m.status (success or failure
// phrasing, chosen by the caller so each copy site can stay specific).
type clipboardWroteMsg struct {
	ok     bool
	status string
	err    error
}

// writeClipboardCmd performs clipboard.WriteAll off the event loop and emits
// a clipboardWroteMsg. successMsg is the status string used when the write
// succeeds; on failure a generic "Failed to copy" is emitted instead, matching
// the pre-refactor wording the user already sees.
func writeClipboardCmd(text, successMsg string) tea.Cmd {
	return func() tea.Msg {
		if err := clipboardWrite(text); err != nil {
			return clipboardWroteMsg{ok: false, status: "Failed to copy", err: err}
		}
		return clipboardWroteMsg{ok: true, status: successMsg}
	}
}
