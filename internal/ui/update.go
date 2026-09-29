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
	gen := m.statusGen
	model, cmd := m.handle(msg)
	if m.statusGen != gen && m.status != "" {
		cmd = tea.Batch(cmd, m.expireStatus())
	}
	return model, cmd
}

func (m *model) handle(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case statusExpiredMsg:
		if msg.gen == m.statusGen {
			m.status, m.statusErr = "", false
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.SetWidth(max(msg.Width-2, 10))
		m.clamp(tabInstalled)
		m.clamp(tabBrowse)
		return m, nil
	case tea.BackgroundColorMsg:
		if m.themeMode == "" || m.themeMode == "auto" {
			m.applyTheme(themeFor(msg.IsDark(), m.palettes))
		}
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
	case rollbackMsg:
		return m.onRollback(msg), true
	case versionsMsg:
		return m.onVersions(msg), true
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
		return m.loadExplain(msg.d), true
	case reviewMsg:
		onReview(msg)
	case explainMsg:
		msg.d.explain = &msg.explain
	case readmeMsg:
		m.onReadme(msg)
	case releasesMsg:
		m.onReleases(msg)
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
	case historyMsg:
		onHistory(msg)
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
	m.toggling--
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

// spinning reports whether anything the status line shows is in progress.
func (m *model) spinning() bool {
	return m.busy != "" || m.indexLoading || m.checking || (m.detail != nil && m.detail.loading) ||
		(m.review != nil && m.review.loading() > 0)
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
		// The first ctrl+c cancels a running operation, and a second quits.
		if m.cancelOperation() {
			return m, nil
		}
		return m, tea.Quit
	}
	if c := m.confirm; c != nil {
		m.confirm = nil
		if m.keys.is(k, actConfirm) {
			return m, m.withSpinner(c.run())
		}
		m.setStatus("Cancelled", false)
		return m, nil
	}
	switch m.screen {
	case screenOutput:
		return m.keyOutput(m.keys.action(k, onOutput))
	case screenDetail:
		return m.keyDetail(k)
	case screenReview:
		return m.keyReview(m.keys.action(k, onList))
	case screenHistory:
		return m.keyHistory(m.keys.action(k, onList))
	case screenList:
	}

	if f := &m.filters[m.tab]; f.Focused() {
		switch a := m.keys.typing(k); a {
		case actOpen:
			// As in fzf: type, move to a result, and open it.
			f.Blur()
			return m.listAction(actOpen)
		case actSwitch:
			f.Blur()
			return m, nil
		case actClose:
			f.Blur()
			m.setFilter("")
			return m, nil
		case actUp, actDown, actPageUp, actPageDown:
			m.move(a)
			return m, nil
		default:
		}
		before := f.Value()
		var cmd tea.Cmd
		*f, cmd = f.Update(msg)
		if f.Value() != before {
			m.cursor[m.tab], m.offset[m.tab] = 0, 0
		}
		return m, cmd
	}
	return m.listAction(m.keys.action(k, onList))
}

func (m *model) setFilter(value string) {
	m.filters[m.tab].SetValue(value)
	m.cursor[m.tab], m.offset[m.tab] = 0, 0
}

func (m *model) listAction(a action) (tea.Model, tea.Cmd) {
	switch a {
	case actQuit:
		return m.quit()
	case actClose:
		if m.filters[m.tab].Value() != "" {
			m.setFilter("")
			return m, nil
		}
		return m.quit()
	case actSwitch:
		m.tab = 1 - m.tab
		return m, nil
	case actTabInstalled:
		m.tab = tabInstalled
		return m, nil
	case actTabMarketplace:
		m.tab = tabBrowse
		return m, nil
	case actSearch:
		return m, m.filters[m.tab].Focus()
	case actHelp:
		m.showHelp = !m.showHelp
		m.clamp(m.tab)
		return m, nil
	case actUp, actDown, actPageUp, actPageDown, actTop, actBottom:
		m.move(a)
		return m, nil
	case actHomepage:
		return m, m.openHomepage()
	case actOutput:
		switch {
		case m.live != nil:
			m.screen, m.page, m.outputFollow = screenOutput, nil, true
		case m.output.title != "":
			m.screen, m.page, m.outputOffset, m.outputFollow = screenOutput, &m.output, 0, false
		}
		return m, nil
	case actHistory:
		return m, m.openHistory()
	default:
	}
	if m.tab == tabInstalled {
		return m.installedAction(a)
	}
	return m.browseAction(a)
}

func (m *model) quit() (tea.Model, tea.Cmd) {
	if m.busy != "" {
		m.setStatus(m.busy+" is still running; wait for it, or press ctrl+c to cancel it", true)
		return m, nil
	}
	return m, tea.Quit
}

func (m *model) installedAction(a action) (tea.Model, tea.Cmd) {
	switch a {
	case actReload:
		m.setStatus("", false)
		return m, tea.Batch(m.loadInstalled(), m.loadVersion())
	case actCheck:
		if len(m.installed) == 0 || m.checking {
			return m, nil
		}
		return m, m.withSpinner(m.checkUpdates())
	case actUpdateAll:
		list := m.available()
		if len(list) == 0 {
			failed, unchecked := m.checkGaps()
			switch {
			case m.checking:
				// The status line then shows the check in progress.
				m.setStatus("", false)
			case failed > 0:
				m.setStatus("No updates to apply: "+plural(failed, "check")+" failed", true)
			case unchecked > 0:
				m.setStatus("No updates to apply: "+plural(unchecked, "plugin")+" not checked", true)
			default:
				m.setStatus("No updates available", false)
			}
			return m, nil
		}
		if !m.idle() {
			return m, nil
		}
		return m, m.openReview(list)
	default:
	}
	p, ok := m.selectedInstalled()
	if !ok {
		return m, nil
	}
	switch a {
	case actOpen:
		return m, m.openInstalled(p)
	case actToggle:
		return m, m.toggle(p)
	case actUpdate:
		return m, m.openUpdate(p)
	case actUninstall:
		m.askUninstall(p)
	case actRollback:
		return m, m.rollback(p)
	default:
	}
	return m, nil
}

// rollback plans undoing the last change to p, which a linked plugin never
// has.
func (m *model) rollback(p herdr.InstalledPluginInfo) tea.Cmd {
	if !m.idle() {
		return nil
	}
	return m.planRollback(p)
}

// toggle enables or disables p. It waits for a running operation: herdr
// rewrites its plugin list as it installs, and an install over p leaves it
// enabled or disabled as it was before, undoing a change made meanwhile.
func (m *model) toggle(p herdr.InstalledPluginInfo) tea.Cmd {
	if p.Enabled && p.PluginID == m.opts.SelfID {
		m.setStatus(manager.ErrSelf.Error(), true)
		return nil
	}
	if m.busy != "" {
		m.setStatus(m.busy+" is still running; enable or disable plugins once it ends", true)
		return nil
	}
	m.toggling++
	return m.setEnabled(p.PluginID, !p.Enabled)
}

func (m *model) browseAction(a action) (tea.Model, tea.Cmd) {
	switch a {
	case actReload:
		if m.indexLoading {
			return m, nil
		}
		m.indexLoading = true
		m.setStatus("", false)
		return m, m.withSpinner(m.loadIndex(true))
	case actSort:
		m.order = m.order.Next()
		m.sortEntries()
		m.cursor[tabBrowse], m.offset[tabBrowse] = 0, 0
		return m, nil
	case actOpen:
		entries := m.visibleEntries()
		if c := m.cursor[tabBrowse]; c < len(entries) {
			return m, m.openInstall(entries[c])
		}
		if src, ok := m.typedSource(); ok {
			return m, m.openPreview(src.String(), &installTarget{src: src}, nil)
		}
	default:
	}
	return m, nil
}

func (m *model) keyDetail(k string) (tea.Model, tea.Cmd) {
	d := m.detail
	if d.versions != nil {
		return m, m.keyVersions(d, m.keys.action(k, onPicker))
	}
	a := m.keys.action(k, onDetail)
	offset := &d.offsets[d.view]
	switch a {
	case actBack, actQuit:
		m.screen, m.detail = screenList, nil
		if d.review != nil {
			m.screen = screenReview
		}
		return m, nil
	case actSwitch:
		return m, m.switchView(d)
	case actHomepage:
		return m, m.openHomepage()
	case actUp:
		*offset = max(*offset-1, 0)
		return m, nil
	case actDown:
		*offset++
		return m, nil
	case actPageUp:
		*offset = max(*offset-m.bodyHeight(), 0)
		return m, nil
	case actPageDown:
		*offset += m.bodyHeight()
		return m, nil
	case actTop:
		*offset = 0
		return m, nil
	default:
	}

	if d.plugin != nil {
		p := *d.plugin
		switch a {
		case actToggle:
			return m, m.toggle(p)
		case actUpdate:
			return m, m.openUpdate(p)
		case actUninstall:
			m.askUninstall(p)
			return m, nil
		case actRollback:
			return m, m.rollback(p)
		case actVersion:
			if d.loading || !m.idle() {
				return m, nil
			}
			return m, m.openInstalledVersions(d)
		case actReload:
			return m, m.loadLogs(p.PluginID)
		default:
		}
		return m, nil
	}

	if a == actVersion && d.install != nil && !d.loading {
		return m, m.openVersions(d)
	}
	// A preview's action has its own key, so the key that opened the
	// preview cannot also install it when pressed twice.
	want, verb := changeKey(d)
	if a == actOpen && d.preview != nil {
		m.setStatus("Press "+m.keys.name(want)+" to "+verb, false)
		return m, nil
	}
	if a != want || d.loading || d.preview == nil {
		return m, nil
	}
	if d.review != nil {
		m.setStatus("The review applies its updates together."+m.press(actBack, "to return to it"), false)
		return m, nil
	}
	if len(d.preview.Problems) > 0 {
		m.setStatus("This plugin cannot be installed here; see Problems", true)
		return m, nil
	}
	if !m.idle() {
		return m, nil
	}
	if c := d.change; c != nil {
		change := *c
		if change.fromPreview {
			change.target.Ref, change.target.Commit = d.preview.Ref, d.preview.Commit
		}
		return m, m.withSpinner(m.applyChange(change))
	}
	target := *d.install
	target.ref, target.commit = d.preview.Ref, d.preview.Commit
	return m, m.withSpinner(m.install(target, d.preview.Manifest.ID, d.preview.Existing))
}

// changeKey is the key that applies a preview and what it does. An update
// applies with the key that opened it; an install and any other change with
// the install key.
func changeKey(d *detail) (a action, verb string) {
	switch {
	case d.change == nil:
		return actInstall, "install"
	case d.change.kind == manager.KindUpdate:
		return actUpdate, "update"
	}
	return actInstall, changeVerbs[d.change.kind][0]
}

// keyOutput scrolls the output; moving to its end follows what herdr still
// prints. Any other key goes back, leaving a running operation running.
func (m *model) keyOutput(a action) (tea.Model, tea.Cmd) {
	switch a {
	case actUp:
		m.outputOffset = max(m.outputOffset-1, 0)
	case actDown:
		m.outputOffset++
	case actPageUp:
		m.outputOffset = max(m.outputOffset-m.bodyHeight(), 0)
	case actPageDown:
		m.outputOffset += m.bodyHeight()
	case actRetry:
		if p := m.page; m.live == nil && p != nil && p.retry != nil {
			if !m.idle() {
				return m, nil
			}
			m.page = nil
			return m, p.retry()
		}
		fallthrough
	default:
		m.screen = screenList
		if p := m.page; m.live == nil && p != nil && p.back == screenHistory && m.history != nil {
			m.screen = screenHistory
		}
		return m, nil
	}
	m.outputFollow = m.outputOffset >= m.outputMax
	return m, nil
}

// idle reports whether a new operation may start, explaining why not on the
// status line.
func (m *model) idle() bool {
	switch {
	case m.busy != "":
		m.setStatus(m.busy+" is still running", true)
		return false
	case m.toggling > 0:
		m.setStatus("A plugin is still being enabled or disabled", true)
		return false
	}
	return true
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
	c := updateChange(ch)
	d := &detail{crumb: tabNames[tabInstalled], title: "Update " + p.Name, loading: true, change: &c}
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
func (m *model) move(a action) {
	n := m.rowCount()
	c := m.cursor[m.tab]
	page := m.pageSize()
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
		c = n - 1
	default:
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
