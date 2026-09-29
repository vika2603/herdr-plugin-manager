package ui

import (
	"strconv"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Rows of the screens as frame lays them out: see listChrome and
// detailChrome.
const (
	listTabsRow   = 0
	listBodyTop   = 6
	detailTabsRow = 2
	detailBodyTop = 5
	// versionsTop is the picker's first version below its heading and gap.
	versionsTop = 2
	// wheelLines is how far a wheel notch scrolls text.
	wheelLines = 3
)

// wheel scrolls: a list moves its selection, text moves by a few lines.
func (m *model) wheel(ms tea.Mouse) {
	if m.confirm != nil || ms.Button != tea.MouseWheelUp && ms.Button != tea.MouseWheelDown {
		return
	}
	up := ms.Button == tea.MouseWheelUp
	a, lines := actDown, wheelLines
	if up {
		a, lines = actUp, -wheelLines
	}
	switch m.screen {
	case screenList:
		m.move(a)
	case screenDetail:
		if d := m.detail; d.versions != nil {
			if ms.X >= d.versions.listWidth {
				d.versions.notesOffset = max(d.versions.notesOffset+lines, 0)
			} else {
				m.keyVersions(d, a)
			}
		} else {
			d.offsets[d.view] = max(d.offsets[d.view]+lines, 0)
		}
	case screenOutput:
		m.outputOffset = max(m.outputOffset+lines, 0)
	}
}

// click selects what is under the pointer; clicking the selected item again
// opens it.
func (m *model) click(ms tea.Mouse) tea.Cmd {
	if m.confirm != nil || ms.Button != tea.MouseLeft {
		return nil
	}
	switch m.screen {
	case screenList:
		return m.clickList(ms)
	case screenDetail:
		return m.clickDetail(ms)
	case screenOutput:
	}
	return nil
}

func (m *model) clickList(ms tea.Mouse) tea.Cmd {
	if ms.Y == listTabsRow {
		if i := tabAt(tabNames[:], []int{len(m.installed), len(m.entries)}, ms.X); i >= 0 {
			m.tab = tab(i)
		}
		return nil
	}
	row := ms.Y - listBodyTop
	if row < 0 || row%itemHeight == itemHeight-1 || row/itemHeight >= m.pageSize() {
		return nil
	}
	if list, pane := m.browseColumns(); m.tab == tabBrowse && pane > 0 && ms.X >= list {
		return nil
	}
	i := m.offset[m.tab] + row/itemHeight
	if i >= m.rowCount() {
		return nil
	}
	m.filters[m.tab].Blur()
	if i != m.cursor[m.tab] {
		m.cursor[m.tab] = i
		return nil
	}
	_, cmd := m.listAction(actOpen)
	return cmd
}

func (m *model) clickDetail(ms tea.Mouse) tea.Cmd {
	d := m.detail
	if vp := d.versions; vp != nil {
		row := ms.Y - detailBodyTop - versionsTop
		i := vp.offset + row
		switch {
		case ms.X >= vp.listWidth || row < 0 || i >= len(vp.rows):
		case i == vp.cursor:
			return m.chooseVersion(d, vp.rows[i])
		default:
			m.selectVersion(d, i)
		}
		return nil
	}
	if ms.Y == detailTabsRow {
		if i := tabAt(d.views(), nil, ms.X); i >= 0 && view(i) != d.view {
			return m.switchView(d)
		}
	}
	return nil
}

// tabAt is the tab tabRow draws at column x, or -1.
func tabAt(names []string, counts []int, x int) int {
	start := 1
	for i := range names {
		width := ansi.StringWidth(tabCell(names, counts, i))
		if x >= start && x < start+width {
			return i
		}
		start += width + 2
	}
	return -1
}

// tabCell is the text of tab i, its name and count padded by a space.
func tabCell(names []string, counts []int, i int) string {
	cell := " " + names[i] + " "
	if counts != nil {
		cell += strconv.Itoa(counts[i]) + " "
	}
	return cell
}
