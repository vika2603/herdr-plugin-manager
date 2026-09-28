package ui

import (
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/vika2603/herdr-plugin-manager/internal/market"
	"github.com/vika2603/herdr-plugin-manager/internal/safe"
	"github.com/vika2603/herdr-plugin-manager/internal/updates"
)

// versionPicker lists what an install preview can switch to: the release
// tags, the newest first, then the default branch by name. Beside the list
// are the release notes of the selected version.
type versionPicker struct {
	refs   []string
	cursor int
	// offset is the first version in view, notesOffset the first line of
	// notes.
	offset, notesOffset int
	// listWidth is the list column's width at the last render, for clicks.
	listWidth int

	loading bool
	// rendered caches the notes of each tag for renderedWidth.
	rendered      map[string][]string
	renderedWidth int
}

// cachedReleases is a repository's releases read earlier in the session.
type cachedReleases struct {
	list []market.Release
	err  error
}

type releasesMsg struct {
	d    *detail
	key  string
	list []market.Release
	err  error
}

const (
	// minNotesWidth is the narrowest notes column worth drawing.
	minNotesWidth = 30
	// columnGap separates the version list from the notes.
	columnGap = 3
)

// openVersions opens the picker on the version the preview shows, and reads
// the repository's release notes unless they were read before.
func (m *model) openVersions(d *detail) tea.Cmd {
	p := d.preview
	if d.install == nil || p == nil || len(p.Releases) == 0 && p.DefaultBranch == "" {
		m.setStatus("No other versions to choose from", false)
		return nil
	}
	vp := &versionPicker{refs: append([]string(nil), p.Releases...), rendered: map[string][]string{}}
	if p.DefaultBranch != "" {
		vp.refs = append(vp.refs, p.DefaultBranch)
	}
	shown := shownRef(d)
	for i, ref := range vp.refs {
		if ref == shown {
			vp.cursor = i
		}
	}
	d.versions = vp
	d.view, d.offsets[viewInfo] = viewInfo, 0
	m.showVersion(d)
	key := d.install.src.Repository()
	if _, ok := m.releases[key]; ok || len(p.Releases) == 0 {
		return nil
	}
	vp.loading = true
	src := d.install.src
	return m.withSpinner(func() tea.Msg {
		list, err := m.b.Releases(m.ctx, src)
		return releasesMsg{d: d, key: key, list: list, err: err}
	})
}

// onReleases keeps the releases for the session. A failure is kept too, so
// the picker does not ask GitHub again on every open while it is limited.
func (m *model) onReleases(msg releasesMsg) {
	m.releases[msg.key] = cachedReleases{list: msg.list, err: msg.err}
	if vp := msg.d.versions; vp != nil {
		vp.loading = false
	}
}

func shownRef(d *detail) string {
	if d.preview.Ref == "" {
		return d.preview.DefaultBranch
	}
	return d.preview.Ref
}

// listRows is how many versions fit below the heading.
func (m *model) listRows() int {
	return max(m.bodyHeight()-versionsTop, 1)
}

// showVersion scrolls the list so the selected version is in view.
func (m *model) showVersion(d *detail) {
	vp := d.versions
	rows := m.listRows()
	switch {
	case vp.cursor < vp.offset:
		vp.offset = vp.cursor
	case vp.cursor >= vp.offset+rows:
		vp.offset = vp.cursor - rows + 1
	}
}

func (m *model) keyVersions(d *detail, a action) tea.Cmd {
	vp := d.versions
	switch a {
	case actUp:
		m.selectVersion(d, vp.cursor-1)
	case actDown:
		m.selectVersion(d, vp.cursor+1)
	case actPageUp:
		vp.notesOffset = max(vp.notesOffset-m.listRows(), 0)
	case actPageDown:
		vp.notesOffset += m.listRows()
	case actOpen:
		return m.chooseVersion(d, vp.refs[vp.cursor])
	case actBack, actQuit, actVersion:
		d.versions = nil
	default:
	}
	return nil
}

func (m *model) selectVersion(d *detail, i int) {
	vp := d.versions
	i = min(max(i, 0), len(vp.refs)-1)
	if i != vp.cursor {
		vp.cursor, vp.notesOffset = i, 0
	}
	m.showVersion(d)
}

// chooseVersion reloads the preview at ref. The README follows the version,
// so it is read again when asked for.
func (m *model) chooseVersion(d *detail, ref string) tea.Cmd {
	d.versions = nil
	if ref == shownRef(d) {
		return nil
	}
	d.preview, d.err, d.loading = nil, nil, true
	d.readme, d.view, d.offsets = readme{}, viewInfo, [2]int{}
	return m.withSpinner(m.loadPreview(d, d.install.src, ref, ""))
}

// versionLines draws the list and, when the screen is wide enough, the notes
// of the selected version beside it.
func (m *model) versionLines(d *detail) []string {
	left := m.versionList(d)
	vp := d.versions
	widest := 0
	for _, l := range left {
		widest = max(widest, ansi.StringWidth(l))
	}
	// The separator sits at listWidth, after a gap; the notes follow it.
	vp.listWidth = widest + columnGap
	notesWidth := m.w() - vp.listWidth - 2
	if notesWidth < minNotesWidth {
		vp.listWidth = m.w()
		return left
	}
	right := m.notesColumn(d, notesWidth)
	sep := m.theme.rule.Render("│") + " "
	out := make([]string, m.bodyHeight())
	for i := range out {
		var l, r string
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out[i] = l + strings.Repeat(" ", vp.listWidth-ansi.StringWidth(l)) + sep + r
	}
	return out
}

// versionList is the version column: the release install picks by default
// is marked latest, and the one the preview shows now is marked shown.
func (m *model) versionList(d *detail) []string {
	t := m.theme
	vp := d.versions
	p := d.preview
	shown := shownRef(d)
	latest := latestRelease(p.Releases)
	out := []string{m.heading("Versions", "", t.faint), ""}
	end := min(vp.offset+m.listRows(), len(vp.refs))
	for i := vp.offset; i < end; i++ {
		ref := vp.refs[i]
		label := ref
		if ref == p.DefaultBranch && i == len(vp.refs)-1 {
			label = "default branch (" + ref + ")"
		}
		style, bar := t.text, "   "
		if i == vp.cursor {
			style, bar = t.bold, " "+t.selBar.Render(glyphSelected)+" "
		}
		line := bar + style.Render(label)
		switch {
		case ref == latest:
			line += "  " + t.ok.Render("latest")
		case updates.IsPrerelease(ref):
			line += "  " + mark(t.faint, glyphPre, "pre-release")
		}
		if ref == shown {
			line += "  " + t.faint.Render("· shown")
		}
		out = append(out, line)
	}
	return out
}

// notesColumn is the heading and release notes of the selected version.
func (m *model) notesColumn(d *detail, width int) []string {
	t := m.theme
	vp := d.versions
	ref := vp.refs[vp.cursor]
	say := func(style lipgloss.Style, text string) []string {
		return []string{t.bold.Render(ref), "", style.Render(text)}
	}
	if ref == d.preview.DefaultBranch && vp.cursor == len(vp.refs)-1 {
		return say(t.faint, "The default branch as it is now. It has no release notes.")
	}
	cached, ok := m.releases[d.install.src.Repository()]
	switch {
	case vp.loading || !ok:
		return say(t.faint, "Reading the release notes…")
	case errors.Is(cached.err, market.ErrRateLimited):
		return say(t.warn, glyphWarning+" "+market.ErrRateLimited.Error())
	case cached.err != nil:
		return say(t.err, glyphFailed+" "+oneLine(cached.err.Error()))
	}
	var rel *market.Release
	for i := range cached.list {
		if cached.list[i].Tag == ref {
			rel = &cached.list[i]
		}
	}
	if rel == nil {
		return say(t.faint, "This tag has no GitHub release, so no notes.")
	}
	heading := t.bold.Render(ref)
	if rel.Name != "" && rel.Name != ref {
		heading += "  " + t.text.Render(rel.Name)
	}
	if !rel.PublishedAt.IsZero() {
		heading += t.faint.Render(" · " + rel.PublishedAt.Format("2006-01-02"))
	}
	if strings.TrimSpace(rel.Notes) == "" {
		return []string{heading, "", t.faint.Render("This release has no notes.")}
	}
	if vp.renderedWidth != width {
		vp.rendered, vp.renderedWidth = map[string][]string{}, width
	}
	lines, ok := vp.rendered[ref]
	if !ok {
		lines = append(m.render(rel.Notes, width), "", indent+t.faint.Render(safe.Line(rel.URL)))
		vp.rendered[ref] = lines
	}
	vp.notesOffset = min(vp.notesOffset, max(len(lines)-m.listRows(), 0))
	return append([]string{heading, ""}, lines[vp.notesOffset:]...)
}
