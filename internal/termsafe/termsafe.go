// Package termsafe strips the escape sequences that let remote content act on
// the terminal instead of only colouring it.
//
// Everything lazylab renders comes from a GitLab server: job traces, file
// contents, project names, branch names, merge request bodies. Any person who
// can open a merge request or push a branch chooses those bytes. Passed through
// verbatim they reach the terminal as instructions, not as text. OSC 52 writes
// the operator's system clipboard with no prompt, which matters most here
// because lazylab's own workflow is to yank a glab command and paste it into a
// shell. CSI 6n makes the terminal reply on stdin as though the operator typed.
//
// [Filter] keeps SGR (colour and text attributes) and drops every other
// sequence. Colour survives because GitLab job traces rely on it and a log pane
// without it would be unreadable. The residual risk of keeping SGR is that a
// payload can set a foreground colour equal to the background and hide text,
// which is a legibility trick rather than terminal control, and worth the trade.
package termsafe

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Filter returns s with every escape sequence removed except SGR, and with the
// control bytes that reposition or overwrite output removed. Newline and tab
// survive because they carry layout that the panes depend on.
func Filter(s string) string {
	if !mayCarryControl(s) {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	p := ansi.GetParser()
	defer ansi.PutParser(p)

	var state byte
	for rest := s; len(rest) > 0; {
		seq, _, n, newState := ansi.DecodeSequence(rest, state, p)
		// A zero-length decode would spin forever, so drop the byte it stalled on.
		if n <= 0 {
			rest = rest[1:]
			continue
		}
		if keep(seq) {
			b.WriteString(seq)
		}
		rest = rest[n:]
		state = newState
	}
	return b.String()
}

// mayCarryControl reports whether s holds any byte that Filter could remove. It
// is a byte scan, so a UTF-8 continuation byte reads as a C1 control and sends
// plain non-ASCII text down the parser path. That costs a scan and never a
// wrong answer, because the parser decodes such a byte back into its rune.
func mayCarryControl(s string) bool {
	for i := range len(s) {
		if isStrippableByte(s[i]) {
			return true
		}
	}
	return false
}

// isStrippableByte reports whether b is a control byte Filter removes. Newline
// and tab are excluded because they are layout, not control. 0x80 to 0x9f are
// the C1 range, where a bare 0x9b acts as a CSI introducer on many terminals.
func isStrippableByte(b byte) bool {
	switch {
	case b == '\n', b == '\t':
		return false
	case b < 0x20, b == 0x7f:
		return true
	case b >= 0x80 && b <= 0x9f:
		return true
	}
	return false
}

// keep reports whether a decoded sequence may reach the terminal.
func keep(seq string) bool {
	if seq == "" {
		return false
	}
	if ansi.HasCsiPrefix(seq) {
		return isSGR(seq)
	}
	if ansi.HasEscPrefix(seq) || ansi.HasOscPrefix(seq) ||
		ansi.HasDcsPrefix(seq) || ansi.HasApcPrefix(seq) ||
		ansi.HasPmPrefix(seq) || ansi.HasSosPrefix(seq) {
		return false
	}
	// The length guard is what makes the C1 range in isStrippableByte safe: a
	// multi-byte rune reaches here whole, so its continuation bytes are never
	// tested individually. Only a lone byte the decoder could not widen is.
	if len(seq) == 1 {
		return !isStrippableByte(seq[0])
	}
	return true
}

// isSGR reports whether a CSI sequence sets graphic rendition, the one class
// Filter admits.
func isSGR(seq string) bool {
	return seq[len(seq)-1] == 'm'
}
