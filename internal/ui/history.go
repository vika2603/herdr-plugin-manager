package ui

import (
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/vika2603/herdr-plugin-manager/internal/manager"
	"github.com/vika2603/herdr-plugin-manager/internal/safe"
)

// historyView is the list of recorded changes, the newest first.
type historyView struct {
	entries        []manager.Entry
	err            error
	loaded         bool
	cursor, offset int
}

type historyMsg struct {
	h       *historyView
	entries []manager.Entry
	err     error
}

func (m *model) openHistory() tea.Cmd {
	h := &historyView{}
	m.history, m.screen = h, screenHistory
	return func() tea.Msg {
		entries, err := m.b.HistoryEntries()
		slices.Reverse(entries)
		return historyMsg{h: h, entries: entries, err: err}
	}
}

func onHistory(msg historyMsg) {
	msg.h.entries, msg.h.err, msg.h.loaded = msg.entries, msg.err, true
}

func (m *model) keyHistory(a action) (tea.Model, tea.Cmd) {
	h := m.history
	switch a {
	case actUp, actDown, actPageUp, actPageDown, actTop, actBottom:
		m.moveHistory(a)
	case actOpen:
		if h.cursor < len(h.entries) {
			m.openEntry(h.entries[h.cursor])
		}
	case actReload:
		return m, m.openHistory()
	case actClose, actQuit:
		m.screen, m.history = screenList, nil
	default:
	}
	return m, nil
}

func (m *model) moveHistory(a action) {
	h := m.history
	page := m.pageSize()
	c := h.cursor
	switch a {
	case actUp:
		c--
	case actDown:
		c++
	case actPageUp:
		c -= page
	case actPageDown:
		c += page
	case actTop:
		c = 0
	case actBottom:
		c = len(h.entries) - 1
	default:
	}
	h.cursor = min(max(c, 0), max(len(h.entries)-1, 0))
	if h.cursor < h.offset {
		h.offset = h.cursor
	}
	if h.cursor >= h.offset+page {
		h.offset = h.cursor - page + 1
	}
}

// openEntry shows a recorded change in full: the plugin's state around it
// and everything herdr printed during it.
func (m *model) openEntry(e manager.Entry) {
	lines := e.Details()
	log, err := manager.ReadLog(e)
	switch {
	case err != nil:
		lines = append(lines, "", "output: "+err.Error())
	case log != "":
		lines = append(lines, "", log)
	}
	title := string(e.Kind) + " " + e.Plugin + " at " + e.Time.Local().Format(time.DateTime)
	m.page = &output{title: title, text: strings.Join(lines, "\n"), retry: m.retryEntry(e), back: screenHistory}
	m.screen, m.outputOffset, m.outputFollow = screenOutput, 0, false
}

func (m *model) viewHistory() string {
	return m.frame(m.crumbs(tabNames[tabInstalled], "History"), nil, m.historyItems())
}

// historyItems are the changes: the plugin, what was done and how it
// ended, then its state before and after, or why it failed.
func (m *model) historyItems() []string {
	t := m.theme
	h := m.history
	switch {
	case !h.loaded:
		return []string{indent + t.faint.Render("Reading the history…")}
	case h.err != nil:
		return []string{indent + t.err.Render(glyphFailed+" "+safe.Line(h.err.Error()))}
	case len(h.entries) == 0:
		return []string{indent + t.faint.Render("No changes recorded yet")}
	}
	var out []string
	end := min(h.offset+m.pageSize(), len(h.entries))
	for i := h.offset; i < end; i++ {
		e := h.entries[i]
		selected := i == h.cursor
		nameStyle, descStyle := m.itemStyles(selected)
		var result string
		switch r := e.Result(); r {
		case "done":
			result = mark(t.ok, glyphDone, r)
		case "cancelled", "unconfirmed":
			result = mark(t.warn, glyphWarning, r)
		default:
			result = mark(t.err, glyphFailed, r)
		}
		title := strings.Join([]string{nameStyle.Render(e.Plugin), t.faint.Render(string(e.Kind)), result,
			t.faint.Render(e.Time.Local().Format(time.DateTime))}, "  ")
		desc := descStyle.Render(safe.Line(e.StateChange()))
		if e.Failed() {
			first, _, _ := strings.Cut(e.Error, "\n")
			desc = t.err.Render(safe.Line(first))
		}
		out = append(out, m.item(title, desc, selected)...)
	}
	return out
}

// historyStatus is the status line of the history.
func (m *model) historyStatus() string {
	t := m.theme
	h := m.history
	if !h.loaded || h.err != nil {
		return ""
	}
	return " " + t.text.Render(plural(len(h.entries), "change")) + t.faint.Render(m.keyHint(actOpen, "shows one with herdr's output"))
}
