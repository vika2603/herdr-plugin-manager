package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/vika2603/herdr-plugin-manager/internal/updates"
)

// versionPicker lists what an install preview can switch to: the release
// tags, the newest first, then the default branch by name.
type versionPicker struct {
	refs   []string
	cursor int
}

// openVersions opens the picker on the version the preview shows.
func (m *model) openVersions(d *detail) {
	p := d.preview
	if d.install == nil || p == nil || len(p.Releases) == 0 && p.DefaultBranch == "" {
		m.setStatus("No other versions to choose from", false)
		return
	}
	vp := &versionPicker{refs: append([]string(nil), p.Releases...)}
	if p.DefaultBranch != "" {
		vp.refs = append(vp.refs, p.DefaultBranch)
	}
	current := p.Ref
	if current == "" {
		current = p.DefaultBranch
	}
	for i, ref := range vp.refs {
		if ref == current {
			vp.cursor = i
		}
	}
	d.versions = vp
	d.view, d.offsets[viewInfo] = viewInfo, 0
	m.showVersion(d)
}

// showVersion scrolls the picker so the selected version is in view.
func (m *model) showVersion(d *detail) {
	line := versionsTop + d.versions.cursor
	off := &d.offsets[viewInfo]
	switch {
	case line < *off:
		*off = line
	case line >= *off+m.bodyHeight():
		*off = line - m.bodyHeight() + 1
	}
}

func (m *model) keyVersions(d *detail, k string) tea.Cmd {
	vp := d.versions
	switch k {
	case "up", "k":
		vp.cursor = max(vp.cursor-1, 0)
		m.showVersion(d)
	case "down", "j":
		vp.cursor = min(vp.cursor+1, len(vp.refs)-1)
		m.showVersion(d)
	case "enter":
		return m.chooseVersion(d, vp.refs[vp.cursor])
	case "esc", "q", "v":
		d.versions = nil
	}
	return nil
}

// chooseVersion reloads the preview at ref. The README follows the version,
// so it is read again when asked for.
func (m *model) chooseVersion(d *detail, ref string) tea.Cmd {
	d.versions = nil
	shown := d.preview.Ref
	if shown == "" {
		shown = d.preview.DefaultBranch
	}
	if ref == shown {
		return nil
	}
	d.preview, d.err, d.loading = nil, nil, true
	d.readme, d.view, d.offsets = readme{}, viewInfo, [2]int{}
	return m.withSpinner(m.loadPreview(d, d.install.src, ref, ""))
}

// versionLines draws the picker: the release the preview would pick by
// default is marked latest, and the one it shows now current.
func (m *model) versionLines(d *detail) []string {
	t := m.theme
	vp := d.versions
	p := d.preview
	shown := p.Ref
	if shown == "" {
		shown = p.DefaultBranch
	}
	latest := ""
	for _, tag := range p.Releases {
		if !updates.IsPrerelease(tag) {
			latest = tag
			break
		}
	}
	out := []string{"  " + t.heading.Render("Choose a version"), ""}
	for i, ref := range vp.refs {
		label := ref
		if ref == p.DefaultBranch && i == len(vp.refs)-1 {
			label = "default branch (" + ref + ")"
		}
		style, bar := t.itemTitle, "   "
		if i == vp.cursor {
			style, bar = t.selTitle, " "+t.selBar.Render("│")+" "
		}
		line := bar + style.Render(label)
		switch {
		case ref == latest:
			line += " " + t.badgeInstalled.Render("latest")
		case updates.IsPrerelease(ref):
			line += " " + t.faint.Render("pre-release")
		}
		if ref == shown {
			line += " " + t.faint.Render("· shown")
		}
		out = append(out, line)
	}
	return out
}
