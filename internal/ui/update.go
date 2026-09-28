package ui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-plugin-manager/internal/manager"
	"github.com/vika2603/herdr-plugin-manager/internal/market"
	"github.com/vika2603/herdr-plugin-manager/internal/source"
	"github.com/vika2603/herdr-plugin-manager/internal/updates"
)

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.SetWidth(max(msg.Width-2, 10))
		m.clamp(tabInstalled)
		m.clamp(tabBrowse)
		return m, nil
	case tea.BackgroundColorMsg:
		m.applyTheme(newTheme(msg.IsDark()))
		return m, nil
	case spinner.TickMsg:
		if !m.spinning() {
			m.ticking = false
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case tea.KeyPressMsg:
		return m.key(msg)
	case tea.MouseWheelMsg:
		m.wheel(tea.Mouse(msg))
		return m, nil
	case tea.MouseClickMsg:
		return m, m.click(tea.Mouse(msg))
	}
	if cmd, ok := m.result(msg); ok {
		return m, cmd
	}
	if f := &m.filters[m.tab]; f.Focused() {
		var cmd tea.Cmd
		*f, cmd = f.Update(msg)
		return m, cmd
	}
	return m, nil
}

// result applies the outcome of a backend call, reporting false for any
// other message.
func (m *model) result(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case installedMsg:
		return m.onInstalled(msg), true
	case versionMsg:
		m.herdrVersion = string(msg)
	case indexMsg:
		m.onIndex(msg)
	case checksMsg:
		if msg.gen != m.checkGen {
			break
		}
		m.checking = false
		m.checks = make(map[string]manager.Checked, len(msg.results))
		for _, ch := range msg.results {
			m.checks[ch.Plugin.PluginID] = ch
		}
	case logsMsg:
		if d := m.detail; d != nil && d.plugin != nil && d.plugin.PluginID == msg.id {
			d.logs, d.logsErr = msg.logs, msg.err
		}
	case previewMsg:
		if m.detail != msg.d {
			break
		}
		msg.d.loading = false
		msg.d.preview, msg.d.err = msg.preview, msg.err
	case readmeMsg:
		m.onReadme(msg)
	case openedMsg:
		if msg.err != nil {
			m.setStatus(msg.err.Error(), true)
		} else {
			m.setStatus("Opened "+msg.url, false)
		}
	case enabledMsg:
		m.onEnabled(msg)
	case opDoneMsg:
		return m.onOpDone(msg), true
	default:
		return nil, false
	}
	return nil, true
}

func (m *model) onInstalled(msg installedMsg) tea.Cmd {
	m.loaded = true
	m.installed, m.installedErr = msg.plugins, msg.err
	m.clamp(tabInstalled)
	if msg.err != nil || len(msg.plugins) == 0 {
		return nil
	}
	return m.withSpinner(m.checkUpdates())
}

func (m *model) onIndex(msg indexMsg) {
	m.indexLoading = false
	m.indexErr = msg.err
	if msg.err != nil {
		return
	}
	m.entries = msg.entries
	m.sortEntries()
	m.indexStatus = msg.status
	m.clamp(tabBrowse)
	if st := msg.status; st.FetchErr != nil {
		m.setStatus(fmt.Sprintf("Index download failed, using the copy from %s: %v", st.FetchedAt.Format(time.DateTime), st.FetchErr), true)
	}
}

func (m *model) onEnabled(msg enabledMsg) {
	if msg.err != nil {
		m.setStatus(msg.err.Error(), true)
		return
	}
	// Commands still running may hold the old slice, so the change goes into
	// a copy rather than into elements they can read.
	m.installed = slices.Clone(m.installed)
	for i := range m.installed {
		if m.installed[i].PluginID == msg.id {
			m.installed[i].Enabled = msg.enabled
		}
	}
	if d := m.detail; d != nil && d.plugin != nil && d.plugin.PluginID == msg.id {
		d.plugin.Enabled = msg.enabled
	}
	m.setStatus(enabledWord(msg.enabled)+" "+msg.id, false)
}

// onOpDone ends an install, update or uninstall: a failure opens herdr's
// output, a success returns to the list. Either way the list is reloaded.
func (m *model) onOpDone(msg opDoneMsg) tea.Cmd {
	m.busy = ""
	m.output = output{title: msg.title, text: msg.output, err: msg.err}
	if msg.err != nil {
		m.setStatus(msg.err.Error(), true)
		m.screen, m.detail, m.outputOffset = screenOutput, nil, 0
	} else {
		m.setStatus(msg.done+" · o shows herdr's output", false)
		if m.screen == screenDetail {
			m.screen, m.detail = screenList, nil
		}
	}
	return m.loadInstalled()
}

// spinning reports whether anything the status line shows is in progress.
func (m *model) spinning() bool {
	return m.busy != "" || m.indexLoading || m.checking || (m.detail != nil && m.detail.loading)
}

// withSpinner starts the spinner along with cmd unless it is already
// ticking.
func (m *model) withSpinner(cmd tea.Cmd) tea.Cmd {
	if m.ticking {
		return cmd
	}
	m.ticking = true
	return tea.Batch(cmd, m.spinner.Tick)
}

func (m *model) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if k == "ctrl+c" {
		return m, tea.Quit
	}
	if c := m.confirm; c != nil {
		m.confirm = nil
		if k == "y" || k == "Y" {
			return m, m.withSpinner(c.run())
		}
		m.setStatus("Cancelled", false)
		return m, nil
	}
	f := &m.filters[m.tab]
	typing := m.screen == screenList && f.Focused()
	k = navAlias(k, typing)
	switch m.screen {
	case screenOutput:
		return m.keyOutput(k)
	case screenDetail:
		return m.keyDetail(k)
	case screenList:
	}

	if typing {
		switch k {
		case "enter":
			// As in fzf: type, move to a result, and enter opens it.
			f.Blur()
			return m.keyList("enter")
		case "tab":
			f.Blur()
			return m, nil
		case "esc":
			f.Blur()
			m.setFilter("")
			return m, nil
		case "up", "down", "pgup", "pgdown":
			m.move(k)
			return m, nil
		}
		before := f.Value()
		var cmd tea.Cmd
		*f, cmd = f.Update(msg)
		if f.Value() != before {
			m.cursor[m.tab], m.offset[m.tab] = 0, 0
		}
		return m, cmd
	}
	return m.keyList(k)
}

// navAlias maps the common editor and pager keys onto the arrow and page
// keys, alongside vim's j and k: ctrl+n and ctrl+p move by a line, ctrl+f and
// ctrl+d page down, ctrl+b and ctrl+u page up. While typing in a filter only
// ctrl+n and ctrl+p apply; the text field keeps the others, ctrl+u among them.
func navAlias(k string, typing bool) string {
	switch k {
	case "ctrl+n":
		return "down"
	case "ctrl+p":
		return "up"
	}
	if typing {
		return k
	}
	switch k {
	case "ctrl+f", "ctrl+d":
		return "pgdown"
	case "ctrl+b", "ctrl+u":
		return "pgup"
	}
	return k
}

func (m *model) setFilter(value string) {
	m.filters[m.tab].SetValue(value)
	m.cursor[m.tab], m.offset[m.tab] = 0, 0
}

func (m *model) keyList(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "q":
		return m.quit()
	case "esc":
		if m.filters[m.tab].Value() != "" {
			m.setFilter("")
			return m, nil
		}
		return m.quit()
	case "tab", "shift+tab":
		m.tab = 1 - m.tab
		return m, nil
	case "1":
		m.tab = tabInstalled
		return m, nil
	case "2":
		m.tab = tabBrowse
		return m, nil
	case "/":
		return m, m.filters[m.tab].Focus()
	case "?":
		m.showHelp = !m.showHelp
		m.clamp(m.tab)
		return m, nil
	case "up", "k", "down", "j", "pgup", "pgdown", "home", "g", "end", "G":
		m.move(k)
		return m, nil
	case "w":
		return m, m.openHomepage()
	case "o":
		if m.output.title != "" {
			m.screen, m.outputOffset = screenOutput, 0
		}
		return m, nil
	}
	if m.tab == tabInstalled {
		return m.keyInstalled(k)
	}
	return m.keyBrowse(k)
}

func (m *model) quit() (tea.Model, tea.Cmd) {
	if m.busy != "" {
		m.setStatus(m.busy+" is still running; wait for it, or press ctrl+c to abort it", true)
		return m, nil
	}
	return m, tea.Quit
}

func (m *model) keyInstalled(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "r":
		m.setStatus("", false)
		return m, tea.Batch(m.loadInstalled(), m.loadVersion())
	case "c":
		if len(m.installed) == 0 || m.checking {
			return m, nil
		}
		return m, m.withSpinner(m.checkUpdates())
	case "U":
		list := m.available()
		if len(list) == 0 {
			m.setStatus("No updates available", false)
			return m, nil
		}
		if !m.idle() {
			return m, nil
		}
		m.confirm = &confirm{
			prompt: fmt.Sprintf("Update %d plugins? Their build commands run again", len(list)),
			run:    func() tea.Cmd { return m.updateAll(list) },
		}
		return m, nil
	}
	p, ok := m.selectedInstalled()
	if !ok {
		return m, nil
	}
	switch k {
	case "enter":
		return m, m.openInstalled(p)
	case "space", "e":
		return m, m.toggle(p)
	case "u":
		return m, m.openUpdate(p)
	case "x", "delete":
		m.askUninstall(p)
	}
	return m, nil
}

func (m *model) toggle(p herdr.InstalledPluginInfo) tea.Cmd {
	if p.Enabled && p.PluginID == m.opts.SelfID {
		m.setStatus(manager.ErrSelf.Error(), true)
		return nil
	}
	return m.setEnabled(p.PluginID, !p.Enabled)
}

func (m *model) keyBrowse(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "r":
		if m.indexLoading {
			return m, nil
		}
		m.indexLoading = true
		m.setStatus("", false)
		return m, m.withSpinner(m.loadIndex(true))
	case "s":
		m.order = m.order.Next()
		m.sortEntries()
		m.cursor[tabBrowse], m.offset[tabBrowse] = 0, 0
		return m, nil
	case "enter", "i":
		entries := m.visibleEntries()
		if c := m.cursor[tabBrowse]; c < len(entries) {
			return m, m.openInstall(entries[c])
		}
		if src, ok := m.typedSource(); ok {
			return m, m.openPreview(src.String(), &installTarget{src: src}, nil)
		}
	}
	return m, nil
}

func (m *model) keyDetail(k string) (tea.Model, tea.Cmd) {
	d := m.detail
	if d.versions != nil {
		return m, m.keyVersions(d, k)
	}
	offset := &d.offsets[d.view]
	switch k {
	case "esc", "q", "backspace", "left", "h":
		m.screen, m.detail = screenList, nil
		return m, nil
	case "tab", "shift+tab":
		return m, m.switchView(d)
	case "w":
		return m, m.openHomepage()
	case "up", "k":
		*offset = max(*offset-1, 0)
		return m, nil
	case "down", "j":
		*offset++
		return m, nil
	case "pgup":
		*offset = max(*offset-m.bodyHeight(), 0)
		return m, nil
	case "pgdown", "space":
		*offset += m.bodyHeight()
		return m, nil
	case "home", "g":
		*offset = 0
		return m, nil
	}

	if d.plugin != nil {
		p := *d.plugin
		switch k {
		case "e":
			return m, m.toggle(p)
		case "u":
			return m, m.openUpdate(p)
		case "x", "delete":
			m.askUninstall(p)
			return m, nil
		case "r":
			return m, m.loadLogs(p.PluginID)
		}
		return m, nil
	}

	if k == "v" && d.install != nil && !d.loading {
		m.openVersions(d)
		return m, nil
	}
	if k != "enter" && k != "i" {
		return m, nil
	}
	if d.loading || d.preview == nil {
		return m, nil
	}
	if len(d.preview.Problems) > 0 {
		m.setStatus("This plugin cannot be installed here; see Problems", true)
		return m, nil
	}
	if !m.idle() {
		return m, nil
	}
	if d.update != nil {
		return m, m.withSpinner(m.update(*d.update))
	}
	target := *d.install
	target.ref, target.commit = d.preview.Ref, d.preview.Commit
	return m, m.withSpinner(m.install(target, d.preview.Manifest.ID))
}

func (m *model) keyOutput(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "up", "k":
		m.outputOffset = max(m.outputOffset-1, 0)
	case "down", "j":
		m.outputOffset++
	case "pgup":
		m.outputOffset = max(m.outputOffset-m.bodyHeight(), 0)
	case "pgdown", "space":
		m.outputOffset += m.bodyHeight()
	default:
		m.screen = screenList
	}
	return m, nil
}

// idle reports whether a new operation may start, explaining why not on the
// status line.
func (m *model) idle() bool {
	if m.busy == "" {
		return true
	}
	m.setStatus(m.busy+" is still running", true)
	return false
}

func (m *model) openInstalled(p herdr.InstalledPluginInfo) tea.Cmd {
	d := &detail{crumb: tabNames[tabInstalled], title: p.Name, plugin: &p}
	m.detail, m.screen = d, screenDetail
	return tea.Batch(m.loadLogs(p.PluginID), m.loadInstalledReadme(d, p))
}

func (m *model) openInstall(e market.Entry) tea.Cmd {
	name := e.Manifest.Name
	if name == "" {
		name = e.Manifest.ID
	}
	return m.openPreview(name, &installTarget{src: e.Source}, &e)
}

// openPreview opens the install preview of t, starting from the
// marketplace listing when there is one.
func (m *model) openPreview(title string, t *installTarget, e *market.Entry) tea.Cmd {
	var hint string
	if e != nil {
		hint = e.Repo.HeadCommit
	}
	d := &detail{crumb: tabNames[tabBrowse], title: title, loading: true, install: t, entry: e}
	m.detail, m.screen = d, screenDetail
	return m.withSpinner(m.loadPreview(d, t.src, "", hint))
}

func (m *model) openUpdate(p herdr.InstalledPluginInfo) tea.Cmd {
	ch, ok := m.checks[p.PluginID]
	switch {
	case !ok:
		if _, github := source.FromInstalled(p); !github {
			m.setStatus(p.PluginID+" is linked locally; update its working tree instead", false)
		} else {
			m.setStatus("No update check result for "+p.PluginID+" yet", false)
		}
		return nil
	case ch.Err != nil:
		m.setStatus(ch.Err.Error(), true)
		return nil
	case ch.Result.Kind != updates.Available:
		m.setStatus(p.PluginID+": "+ch.Result.Describe(), false)
		return nil
	}
	d := &detail{crumb: tabNames[tabInstalled], title: "Update " + p.Name + " (" + ch.Result.Describe() + ")", loading: true, update: &ch}
	m.detail, m.screen = d, screenDetail
	return m.withSpinner(m.loadPreview(d, ch.Result.Source, ch.Result.TargetCommit, ""))
}

// askUninstall opens the confirmation for removing p, or explains on the
// status line why it cannot be removed.
func (m *model) askUninstall(p herdr.InstalledPluginInfo) {
	if p.PluginID == m.opts.SelfID {
		m.setStatus(manager.ErrSelf.Error(), true)
		return
	}
	if !m.idle() {
		return
	}
	verb := "Uninstall"
	if _, github := source.FromInstalled(p); !github {
		verb = "Unlink"
	}
	id := p.PluginID
	m.confirm = &confirm{
		prompt: verb + " " + id + "?",
		run:    func() tea.Cmd { return m.uninstall(id) },
	}
}

func (m *model) selectedInstalled() (herdr.InstalledPluginInfo, bool) {
	list := m.visibleInstalled()
	if c := m.cursor[tabInstalled]; c < len(list) {
		return list[c], true
	}
	return herdr.InstalledPluginInfo{}, false
}

// move applies a navigation key to the current tab's cursor.
func (m *model) move(k string) {
	n := m.rowCount()
	c := m.cursor[m.tab]
	page := m.pageSize()
	switch k {
	case "up", "k":
		c--
	case "down", "j":
		c++
	case "pgup":
		c -= page
	case "pgdown":
		c += page
	case "home", "g":
		c = 0
	case "end", "G":
		c = n - 1
	}
	m.cursor[m.tab] = c
	m.clamp(m.tab)
}

// clamp keeps a tab's cursor on a row and its offset around the cursor.
func (m *model) clamp(t tab) {
	var n int
	if t == tabInstalled {
		n = len(m.visibleInstalled())
	} else {
		n = len(m.visibleEntries())
	}
	c := min(max(m.cursor[t], 0), max(n-1, 0))
	m.cursor[t] = c
	page := m.pageSize()
	o := min(m.offset[t], c)
	if c >= o+page {
		o = c - page + 1
	}
	m.offset[t] = max(min(o, max(n-page, 0)), 0)
}

func matchesInstalled(p herdr.InstalledPluginInfo, query string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		p.PluginID, p.Name, p.Description.ValueOrZero(), manager.SourceLabel(p),
	}, "\n"))
	for term := range strings.FieldsSeq(strings.ToLower(query)) {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}

func enabledWord(enabled bool) string {
	if enabled {
		return "Enabled"
	}
	return "Disabled"
}
