package ui

import (
	"errors"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-plugin-manager/internal/manager"
	"github.com/vika2603/herdr-plugin-manager/internal/safe"
	"github.com/vika2603/herdr-plugin-manager/internal/source"
)

// view is which page of a detail screen shows: the information herdr and the
// manifest give about the plugin, which opens first, or its README.
type view int

const (
	viewInfo view = iota
	viewReadme
)

var viewNames = [...]string{viewInfo: "Info", viewReadme: "README"}

// views are the pages a detail screen offers. README is left out once the
// plugin is known to have none.
func (d *detail) views() []string {
	if errors.Is(d.readme.err, manager.ErrNoReadme) {
		return viewNames[:1]
	}
	return viewNames[:]
}

// switchView moves between Info and README. A marketplace plugin's README
// is only downloaded the first time it is asked for.
func (m *model) switchView(d *detail) tea.Cmd {
	if len(d.views()) == 1 {
		m.setStatus("This plugin has no README", false)
		return nil
	}
	if d.view == viewReadme {
		d.view = viewInfo
		return nil
	}
	d.view = viewReadme
	r := &d.readme
	if r.loading || r.doc != nil || r.err != nil {
		return nil
	}
	switch {
	case d.install != nil && d.preview != nil:
		return m.withSpinner(m.loadRemoteReadme(d, d.install.src, d.preview.Commit))
	case d.install != nil:
		return m.withSpinner(m.loadRemoteReadme(d, d.install.src, ""))
	case d.change != nil:
		return m.withSpinner(m.loadRemoteReadme(d, d.change.target.Source, d.change.target.Commit))
	}
	return nil
}

// readme is a detail screen's README and its rendering, which is kept until
// the width or the theme changes.
type readme struct {
	// key is the source and ref the README is of, or is being read for.
	key     string
	loading bool
	doc     *manager.Readme
	err     error

	lines []string
	width int
	dark  bool
}

type readmeMsg struct {
	d   *detail
	key string
	doc *manager.Readme
	err error
}

// cachedReadme is a README read earlier in the session, or the finding that
// there is none.
type cachedReadme struct {
	doc *manager.Readme
	err error
}

func (m *model) loadInstalledReadme(d *detail, p herdr.InstalledPluginInfo) tea.Cmd {
	// The files change when the plugin is updated, which changes its commit.
	key := "local:" + p.PluginRoot + "@" + p.Source.ValueOrZero().ResolvedCommit.ValueOrZero()
	return m.loadReadme(d, key, func() (*manager.Readme, error) { return m.b.InstalledReadme(p) })
}

// loadRemoteReadme reads a README from GitHub at ref. A README is only read,
// so it need not wait for the preview to resolve the commit it installs.
func (m *model) loadRemoteReadme(d *detail, src source.GitHub, ref string) tea.Cmd {
	return m.loadReadme(d, src.String()+"@"+ref, func() (*manager.Readme, error) {
		return m.b.RemoteReadme(m.ctx, src, ref)
	})
}

func (m *model) loadReadme(d *detail, key string, read func() (*manager.Readme, error)) tea.Cmd {
	d.readme.key = key
	if c, ok := m.readmes[key]; ok {
		d.readme.doc, d.readme.err = c.doc, c.err
		return nil
	}
	d.readme.loading = true
	return func() tea.Msg {
		doc, err := read()
		return readmeMsg{d: d, key: key, doc: doc, err: err}
	}
}

// onReadme shows a README and keeps it, or the absence of one, for the
// session. A failed download is not kept, so opening the plugin again
// retries it.
func (m *model) onReadme(msg readmeMsg) {
	if msg.err == nil || errors.Is(msg.err, manager.ErrNoReadme) {
		m.readmes[msg.key] = cachedReadme{doc: msg.doc, err: msg.err}
	}
	// The detail that asked gets the README even when another screen is
	// shown by now, so it is there on return, unless it has asked for
	// another one since.
	r := &msg.d.readme
	if r.key != msg.key {
		return
	}
	r.loading, r.doc, r.err, r.lines = false, msg.doc, msg.err, nil
	if m.detail == msg.d && errors.Is(msg.err, manager.ErrNoReadme) && msg.d.view == viewReadme {
		msg.d.view = viewInfo
		m.setStatus("This plugin has no README", false)
	}
}

// readmeLines renders the README for the current width and theme.
func (m *model) readmeLines(d *detail) []string {
	t := m.theme
	r := &d.readme
	switch {
	case r.loading || (r.doc == nil && r.err == nil):
		return []string{"  " + t.faint.Render("Loading the README…")}
	case r.err != nil:
		return []string{"  " + t.err.Render(oneLine(r.err.Error()))}
	}
	width := max(m.w()-2, 20)
	if r.lines == nil || r.width != width || r.dark != t.dark {
		r.lines, r.width, r.dark = m.render(r.doc.Markdown, width), width, t.dark
	}
	return append(slices.Clone(r.lines), "", "  "+t.faint.Render(safe.Line(r.doc.Location)))
}

func (m *model) render(markdown string, width int) []string {
	style := "light"
	if m.theme.dark {
		style = "dark"
	}
	renderer, err := glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithWordWrap(width))
	if err != nil {
		return strings.Split(markdown, "\n")
	}
	out, err := renderer.Render(markdown)
	if err != nil {
		return strings.Split(markdown, "\n")
	}
	return strings.Split(strings.Trim(out, "\n"), "\n")
}

// homepage is the GitHub page of what the current screen shows.
func (m *model) homepage() (string, bool) {
	if d := m.detail; m.screen == screenDetail && d != nil {
		switch {
		case d.plugin != nil:
			return installedHomepage(*d.plugin)
		case d.install != nil:
			return d.install.src.WebURL(), true
		case d.change != nil:
			return d.change.target.Source.WebURL(), true
		}
	}
	if m.screen != screenList {
		return "", false
	}
	if m.tab == tabInstalled {
		if p, ok := m.selectedInstalled(); ok {
			return installedHomepage(p)
		}
		return "", false
	}
	entries := m.visibleEntries()
	if c := m.cursor[tabBrowse]; c < len(entries) {
		return entries[c].Source.WebURL(), true
	}
	return "", false
}

func installedHomepage(p herdr.InstalledPluginInfo) (string, bool) {
	src, ok := source.FromInstalled(p)
	if !ok {
		return "", false
	}
	return src.WebURL(), true
}

type openedMsg struct {
	url string
	err error
}

func (m *model) openHomepage() tea.Cmd {
	url, ok := m.homepage()
	if !ok {
		m.setStatus("A locally linked plugin has no homepage", false)
		return nil
	}
	return func() tea.Msg {
		return openedMsg{url: url, err: m.b.OpenURL(m.ctx, url)}
	}
}
