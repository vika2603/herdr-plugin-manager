package ui

import (
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-plugin-manager/internal/manager"
	"github.com/vika2603/herdr-plugin-manager/internal/market"
	"github.com/vika2603/herdr-plugin-manager/internal/safe"
	"github.com/vika2603/herdr-plugin-manager/internal/source"
	"github.com/vika2603/herdr-plugin-manager/internal/updates"
)

// versionPicker lists the versions a plugin can be installed at: the release
// tags, the newest first, then the default branch by name. For an installed
// plugin, reinstalling it and pinning or unpinning it follow. Beside the
// list are the release notes of the selected version.
type versionPicker struct {
	rows   []pickRow
	src    source.GitHub
	cursor int
	// releases are the release tags, for the latest mark.
	releases []string
	// current is the ref the preview shows, or the plugin is installed from,
	// marked with currentMark.
	current, currentMark string
	// offset is the first row in view, notesOffset the first line of notes.
	offset, notesOffset int
	// listWidth is the list column's width at the last render, for clicks.
	listWidth int
	// notesOnly shows the selected version's notes in place of the list,
	// on a screen too narrow for both.
	notesOnly bool

	loading bool
	// rendered caches the notes of each tag for renderedWidth.
	rendered      map[string][]string
	renderedWidth int
}

// showingNotesOnly reports whether the notes are shown in place of the
// list, on a screen width cells wide.
func (vp *versionPicker) showingNotesOnly(width int) bool {
	return vp.notesOnly && vp.listWidth == width
}

// pickRow is a version, or with kind set a change to the installed plugin
// that is not a version of its own.
type pickRow struct {
	ref string
	// branch marks the default branch.
	branch bool
	kind   manager.ChangeKind
	label  string
	about  string
}

// cachedReleases is a repository's releases read earlier in the session.
type cachedReleases struct {
	list []market.Release
	err  error
}

type (
	releasesMsg struct {
		d    *detail
		key  string
		list []market.Release
		err  error
	}
	// versionsMsg is what an installed plugin's repository offers.
	versionsMsg struct {
		d        *detail
		versions manager.Versions
		err      error
	}
)

const (
	// minNotesWidth is the narrowest notes column drawn beside the list;
	// narrower, the notes take the list's place.
	minNotesWidth = 50
	// columnGap separates the version list from the notes.
	columnGap = 3
)

// versionRows are the default branch, then the release tags, the newest
// first as releases lists them.
func versionRows(releases []string, defaultBranch string) []pickRow {
	rows := make([]pickRow, 0, len(releases)+1)
	if defaultBranch != "" {
		rows = append(rows, pickRow{ref: defaultBranch, branch: true})
	}
	for _, tag := range releases {
		rows = append(rows, pickRow{ref: tag})
	}
	return rows
}

// openVersions opens the picker of an install preview on the version it
// shows.
func (m *model) openVersions(d *detail) tea.Cmd {
	p := d.preview
	if d.install == nil || p == nil || len(p.Releases) == 0 && p.DefaultBranch == "" {
		m.setStatus("No other versions to choose from", false)
		return nil
	}
	vp := &versionPicker{
		rows: versionRows(p.Releases, p.DefaultBranch), src: d.install.src, releases: p.Releases,
		current: shownRef(d), currentMark: "shown",
	}
	return m.showPicker(d, vp)
}

// openInstalledVersions lists what an installed plugin's repository offers,
// then opens the picker.
func (m *model) openInstalledVersions(d *detail) tea.Cmd {
	p := *d.plugin
	src, ok := source.FromInstalled(p)
	if !ok {
		m.setStatus(p.PluginID+": "+manager.ErrLocal.Error(), false)
		return nil
	}
	d.loading = true
	return m.withSpinner(func() tea.Msg {
		v, err := m.b.Versions(m.ctx, src)
		return versionsMsg{d: d, versions: v, err: err}
	})
}

func (m *model) onVersions(msg versionsMsg) tea.Cmd {
	d := msg.d
	d.loading = false
	if m.detail != d || d.plugin == nil {
		return nil
	}
	if msg.err != nil {
		m.setStatus("Could not list the versions: "+msg.err.Error(), true)
		return nil
	}
	p := *d.plugin
	src, _ := source.FromInstalled(p)
	tr := manager.TrackingOf(p)
	current := tr.Ref
	if tr.Kind == manager.TrackDefault {
		current = msg.versions.DefaultBranch
	}
	vp := &versionPicker{
		rows: versionRows(msg.versions.Releases, msg.versions.DefaultBranch), src: src, releases: msg.versions.Releases,
		current: current, currentMark: "installed",
	}
	vp.rows = append(vp.rows, pickRow{
		kind: manager.KindReinstall, label: "Reinstall the installed version",
		about: "Install " + manager.RevisionLabel(tr.Ref, tr.Commit) + " again, running its build commands again. It keeps following what it follows now.",
	})
	if tr.Kind == manager.TrackPinned {
		vp.rows = append(vp.rows, pickRow{
			kind: manager.KindUnpin, label: "Unpin",
			about: "Follow what an install picks, the latest release of a plugin at the repository root or else the default branch, installing where it is now. Choose a version above to follow that one instead.",
		})
	} else {
		vp.rows = append(vp.rows, pickRow{
			kind: manager.KindPin, label: "Pin to the installed commit",
			about: "Reinstall " + manager.RevisionLabel("", tr.Commit) + " as a commit pin, which has no updates until it is unpinned.",
		})
	}
	return m.showPicker(d, vp)
}

// showPicker opens vp at the top of the list on the latest stable
// release, else the latest release, else the default branch; the version
// shown or installed is marked, not selected. It reads the repository's
// release notes unless they were read before.
func (m *model) showPicker(d *detail, vp *versionPicker) tea.Cmd {
	vp.rendered = map[string][]string{}
	want := latestRelease(vp.releases)
	if want == "" && len(vp.releases) > 0 {
		want = vp.releases[0]
	}
	vp.cursor, vp.offset = 0, 0
	for i, r := range vp.rows {
		if r.kind == "" && !r.branch && r.ref == want {
			vp.cursor = i
			break
		}
	}
	d.versions = vp
	d.view, d.offsets[viewInfo] = viewInfo, 0
	m.showVersion(d)
	key := vp.src.Repository()
	if _, ok := m.releases[key]; ok || len(vp.releases) == 0 {
		return nil
	}
	vp.loading = true
	src := vp.src
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
		return m.chooseVersion(d, vp.rows[vp.cursor])
	case actSwitch:
		// Too narrow for the notes beside the list, the picker shows one
		// or the other.
		vp.notesOnly = !vp.notesOnly
	case actHelp:
		m.showHelp = !m.showHelp
	case actBack, actQuit, actVersion:
		d.versions = nil
	default:
	}
	return nil
}

func (m *model) selectVersion(d *detail, i int) {
	vp := d.versions
	i = min(max(i, 0), len(vp.rows)-1)
	if i != vp.cursor {
		vp.cursor, vp.notesOffset = i, 0
	}
	m.showVersion(d)
}

// chooseVersion reloads an install preview at the chosen version; the
// README follows the version, so it is read again when asked for. For an
// installed plugin it opens the preview of the change.
//
// The preview opens as a detail of its own, with d as its parent: back
// returns to d with the picker as it was left.
func (m *model) chooseVersion(d *detail, r pickRow) tea.Cmd {
	vp := d.versions
	if d.plugin != nil {
		kind := r.kind
		switch {
		case kind != "":
		case r.ref == vp.current:
			kind = manager.KindReinstall
		default:
			kind = manager.KindSwitch
		}
		cmd := m.openChange(*d.plugin, kind, r.ref)
		if m.detail != d {
			m.detail.parent = d
		}
		return cmd
	}
	if r.ref == shownRef(d) {
		// The version already previewed: the same preview, from which back
		// still returns to the picker.
		child := *d
		child.versions, child.parent = nil, d
		m.detail = &child
		return nil
	}
	target := *d.install
	target.ref, target.commit = r.ref, ""
	child := &detail{crumb: d.crumb, title: d.title, loading: true, install: &target, entry: d.entry, parent: d}
	m.detail = child
	return m.withSpinner(m.loadPreview(child, target.src, r.ref, ""))
}

// openChange opens the preview of a version change of p: the manifest at
// the ref the change asks herdr for.
func (m *model) openChange(p herdr.InstalledPluginInfo, kind manager.ChangeKind, ref string) tea.Cmd {
	if kind == manager.KindUnpin {
		ref = ""
	}
	ref, err := manager.VersionRef(p, kind, ref)
	if err != nil {
		m.setStatus(err.Error(), true)
		return nil
	}
	src, _ := source.FromInstalled(p)
	c := &pendingChange{kind: kind, plugin: p, target: manager.Target{Source: src, Ref: ref}, fromPreview: true}
	if kind == manager.KindReinstall {
		// A reinstall is of the installed commit, which its preview shows.
		c.target.Commit, c.fromPreview = manager.ReinstallCommit(p), false
	}
	d := &detail{crumb: tabNames[tabInstalled], title: changeVerbs[kind][2] + " " + p.Name, loading: true, change: c}
	m.detail, m.screen = d, screenDetail
	return m.withSpinner(m.loadPreview(d, src, ref, ""))
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
		k := m.keys.name(actSwitch)
		if vp.notesOnly {
			var out []string
			if k != "" {
				out = append(out, indent+m.theme.faint.Render(k+" shows the versions"), "")
			}
			for _, l := range m.notesColumn(d, m.w()-len(indent)-1, m.listRows()-len(out)) {
				out = append(out, indent+l)
			}
			return out
		}
		if k != "" {
			left = append(left, "", indent+m.theme.faint.Render(k+" shows the release notes"))
		}
		return left
	}
	right := m.notesColumn(d, notesWidth, m.listRows())
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
// is marked latest, and the one shown or installed now is marked so.
func (m *model) versionList(d *detail) []string {
	t := m.theme
	vp := d.versions
	latest := latestRelease(vp.releases)
	out := []string{m.heading("Versions", "", t.faint), ""}
	end := min(vp.offset+m.listRows(), len(vp.rows))
	for i := vp.offset; i < end; i++ {
		r := vp.rows[i]
		label := r.ref
		switch {
		case r.kind != "":
			label = r.label
		case r.branch:
			label = "default branch (" + r.ref + ")"
		}
		style, bar := t.text, "   "
		if r.kind != "" {
			style = t.fg2
		}
		if i == vp.cursor {
			style, bar = t.bold, " "+t.selBar.Render(glyphSelected)+" "
		}
		line := bar + style.Render(label)
		switch {
		case r.kind != "":
		case r.ref == latest:
			line += "  " + t.ok.Render("latest")
		case updates.IsPrerelease(r.ref):
			line += "  " + mark(t.faint, glyphPre, "pre-release")
		}
		if r.kind == "" && r.ref == vp.current {
			line += "  " + t.faint.Render("· "+vp.currentMark)
		}
		out = append(out, line)
	}
	return out
}

// notesColumn is the heading and release notes of the selected version, or
// what the selected change does.
// notesColumn draws the notes of the selected version width cells wide,
// the notes themselves scrolled within rows lines below their heading.
func (m *model) notesColumn(d *detail, width, rows int) []string {
	t := m.theme
	vp := d.versions
	r := vp.rows[vp.cursor]
	if r.kind != "" {
		return append([]string{t.bold.Render(r.label), ""}, strings.Split(ansi.Wrap(t.fg2.Render(r.about), width, ""), "\n")...)
	}
	ref := r.ref
	say := func(style lipgloss.Style, text string) []string {
		return []string{t.bold.Render(ref), "", style.Render(text)}
	}
	if r.branch {
		return say(t.faint, "The default branch as it is now. It has no release notes.")
	}
	cached, ok := m.releases[vp.src.Repository()]
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
	vp.notesOffset = min(vp.notesOffset, max(len(lines)-rows, 0))
	return append([]string{heading, ""}, lines[vp.notesOffset:]...)
}
