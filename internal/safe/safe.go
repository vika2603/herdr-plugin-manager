// Package safe makes untrusted text printable. Marketplace listings,
// manifests, plugin logs and herdr's output are written by plugin authors,
// and a terminal acts on control characters: an escape sequence can move the
// cursor and repaint the install preview the user is about to confirm, and a
// carriage return or bidirectional override can make one command read as
// another. Everything from those sources passes through here before it is
// drawn.
package safe

import (
	"strconv"
	"strings"
	"unicode"
)

// Line returns s as one printable line: line breaks and tabs become spaces,
// and every other control or formatting character is shown as its Go escape,
// such as \x1b or \u202e.
func Line(s string) string {
	return clean(s, false)
}

// Text is Line for multi-line text: line feeds and tabs are kept.
func Text(s string) string {
	return clean(strings.ReplaceAll(s, "\r\n", "\n"), true)
}

func clean(s string, multiline bool) string {
	if !needsCleaning(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			if multiline {
				b.WriteRune(r)
			} else {
				b.WriteByte(' ')
			}
		case r == '\r' && !multiline:
			b.WriteByte(' ')
		case unsafeRune(r):
			q := strconv.QuoteRuneToASCII(r)
			b.WriteString(q[1 : len(q)-1])
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func needsCleaning(s string) bool {
	for _, r := range s {
		if r == '\n' || r == '\t' || unsafeRune(r) {
			return true
		}
	}
	return false
}

// unsafeRune reports control characters, the Unicode replacement for invalid
// UTF-8, and format characters such as bidirectional overrides and
// zero-width joiners.
func unsafeRune(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == unicode.ReplacementChar
}
