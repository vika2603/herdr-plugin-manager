package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/vika2603/herdr-plugin-manager/internal/manager"
)

// The glyphs of docs/design.md. Every one is a single cell wide, so columns
// line up in any terminal.
const (
	glyphSelected = "▌"
	glyphEnabled  = "●"
	glyphDisabled = "○"
	glyphUpdate   = "↑"
	glyphLocal    = "◆"
	glyphDone     = "✓"
	glyphFailed   = "✕"
	glyphWarning  = "▲"
	glyphPre      = "◇"
	glyphStar     = "★"
)

// Indents: content starts in column 3, what belongs to a section in
// column 5.
const (
	indent    = "  "
	subIndent = "    "
)

// mark is a state: a glyph and a word, in the state's colour.
func mark(style lipgloss.Style, glyph, text string) string {
	return style.Render(glyph + " " + text)
}

// field is one row of a label column: an upper-case label and its value,
// which is drawn already styled.
type field struct {
	label string
	value string
}

// fields draws rows with their labels in one column as wide as the longest,
// wrapping each value in the column after it.
func (m *model) fields(rows []field) []string {
	return m.fieldsIn(rows, m.w())
}

// fieldsIn draws rows as fields does, in a column total cells wide.
func (m *model) fieldsIn(rows []field, total int) []string {
	t := m.theme
	width := 0
	for _, r := range rows {
		width = max(width, len(r.label))
	}
	room := max(total-1-len(indent)-width-2, 20)
	var out []string
	for _, r := range rows {
		lines := strings.Split(ansi.Wrap(r.value, room, ""), "\n")
		label := t.label.Render(strings.ToUpper(r.label) + strings.Repeat(" ", width-len(r.label)))
		out = append(out, indent+label+"  "+lines[0])
		for _, l := range lines[1:] {
			out = append(out, indent+strings.Repeat(" ", width+2)+l)
		}
	}
	return out
}

// heading is a section's label, with its note after it.
func (m *model) heading(title, note string, noteStyle lipgloss.Style) string {
	line := indent + m.theme.label.Render(strings.ToUpper(title))
	if note != "" {
		line += "  " + noteStyle.Render(note)
	}
	return line
}

// section draws a heading and its lines, which are wrapped at the
// section's indent.
func (m *model) section(title, note string, noteStyle, body lipgloss.Style, lines []string) []string {
	out := []string{m.heading(title, note, noteStyle)}
	for _, l := range lines {
		out = append(out, wrapIndented(subIndent+body.Render(l), m.w()-1)...)
	}
	return out
}

// sections draws manager sections, a gap between each. What runs during
// install or in every session is noted in the attention colour.
func (m *model) sections(list []manager.Section) []string {
	t := m.theme
	var out []string
	for _, s := range list {
		out = append(out, "")
		out = append(out, m.section(s.Title, s.Note, t.warn, t.text, s.Lines)...)
	}
	return out
}

// titleLine is a plugin's name, version and id, then its state.
func (m *model) titleLine(name, version, id string, state ...string) string {
	t := m.theme
	meta := version
	if id != "" {
		meta += " · " + id
	}
	parts := []string{indent + t.bold.Render(name), t.faint.Render(meta)}
	for _, s := range state {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "  ")
}
