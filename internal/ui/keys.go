package ui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"

	"github.com/vika2603/herdr-plugin-manager/internal/updates"
)

// helpKeys are the help bar's bindings, labelled with the keys the keymap
// gives each action. Update matches keys through the keymap itself.
type helpKeys struct {
	up, down, page, ends, scroll, back, quit, more, less, filter, search, output,
	details, toggle, update, all, check, remove, reload, logs, refresh, sort, install,
	apply, browse, instTab, open, keep, clear, move, yes, no, cont, home, info, readme,
	version, choose, cancel, notes, step, preview, rollback, applyReview, include, reviewed, leave,
	stop, running, retry, history, entry, doctor, recheck, notesTab key.Binding
}

func newHelpKeys(km keymap) helpKeys {
	// first labels a with its first key, which keeps the bar short.
	first := func(a action, desc string) key.Binding {
		if len(km[a]) == 0 {
			return key.NewBinding(key.WithDisabled())
		}
		return key.NewBinding(key.WithKeys(km[a]...), key.WithHelp(km.name(a), desc))
	}
	every := func(a action, desc string) key.Binding {
		if len(km[a]) == 0 {
			return key.NewBinding(key.WithDisabled())
		}
		return key.NewBinding(key.WithKeys(km[a]...), key.WithHelp(km.label(a), desc))
	}
	pair := func(a, z action, desc string) key.Binding {
		if len(km[a]) == 0 || len(km[z]) == 0 {
			return key.NewBinding(key.WithDisabled())
		}
		return key.NewBinding(key.WithKeys(km[a]...), key.WithHelp(km.name(a)+"/"+km.name(z), desc))
	}
	fixed := func(keys, desc string) key.Binding {
		return key.NewBinding(key.WithKeys(keys), key.WithHelp(keys, desc))
	}
	// While typing, only the keys the search field leaves to the list move.
	var moving []string
	for _, a := range []action{actUp, actDown} {
		for _, k := range km[a] {
			if km.typing(k) == a {
				moving = append(moving, keyLabel(k))
			}
		}
	}
	move := key.NewBinding(key.WithDisabled())
	if len(moving) > 0 {
		move = key.NewBinding(key.WithKeys(moving...), key.WithHelp(strings.Join(moving, "/"), "move"))
	}
	return helpKeys{
		up: every(actUp, "up"), down: every(actDown, "down"),
		page: pair(actPageUp, actPageDown, "page"), ends: pair(actTop, actBottom, "top/bottom"),
		scroll: pair(actUp, actDown, "scroll"), back: first(actBack, "back"), quit: first(actQuit, "quit"),
		more: first(actHelp, "more"), less: first(actHelp, "less"),
		filter: first(actSearch, "filter"), search: first(actSearch, "search"), output: first(actOutput, "last output"),
		details: first(actOpen, "details"), preview: first(actOpen, "preview"), toggle: first(actToggle, "enable/disable"),
		update: first(actUpdate, "update"), all: first(actUpdateAll, "update all"), check: first(actCheck, "check updates"),
		remove: first(actUninstall, "uninstall"), reload: first(actReload, "reload"), logs: first(actReload, "reload logs"),
		refresh: first(actReload, "refresh index"), sort: first(actSort, "sort"), install: first(actInstall, "install"),
		apply: first(actUpdate, "update"), browse: first(actSwitch, "marketplace"), instTab: first(actSwitch, "installed"),
		open: first(actOpen, "open"), keep: first(actSwitch, "keep"), clear: first(actClose, "clear"), move: move,
		yes: first(actConfirm, "confirm"), no: fixed("any other key", "cancel"), cont: fixed("any key", "back"),
		home: first(actHomepage, "homepage"), info: first(actSwitch, "info"), readme: first(actSwitch, "readme"),
		version: first(actVersion, "version"), choose: first(actOpen, "choose"), cancel: first(actBack, "cancel"),
		notes: pair(actPageUp, actPageDown, "scroll notes"), step: pair(actUp, actDown, "move"),
		rollback:    first(actRollback, "roll back"),
		applyReview: first(actUpdate, "update included"), include: first(actToggle, "include/leave out"),
		reviewed: first(actOpen, "details"), leave: first(actClose, "back"),
		stop: fixed("ctrl+c", "cancel"), running: fixed("any key", "back, it keeps running"),
		retry: first(actRetry, "retry"), history: first(actHistory, "history"), entry: first(actOpen, "output"),
		doctor: first(actDoctor, "diagnostics"), recheck: first(actReload, "check again"),
		notesTab: first(actSwitch, "notes"),
	}
}

// keyMap is the help for one screen: a short line, and the columns ? opens.
// The first short binding is the screen's main action and is highlighted.
type keyMap struct {
	short []key.Binding
	full  [][]key.Binding
}

func (k keyMap) ShortHelp() []key.Binding { return k.short }

func (k keyMap) FullHelp() [][]key.Binding {
	if k.full == nil {
		return [][]key.Binding{k.short}
	}
	return k.full
}

// keyMap is the help for the current screen. Every screen's bar ends with
// the help key, which lists all of the screen's keys when some do not fit.
func (m *model) keyMap() keyMap {
	k := m.screenKeys()
	if m.confirm != nil || m.screen == screenList {
		return k
	}
	var full [][]key.Binding
	for chunk := range slices.Chunk(k.short, 4) {
		full = append(full, slices.Clone(chunk))
	}
	full = append(full, []key.Binding{m.helpKeys.less})
	return keyMap{short: append(slices.Clone(k.short), m.moreKey()), full: full}
}

func (m *model) screenKeys() keyMap {
	h := m.helpKeys
	if m.confirm != nil {
		return keyMap{short: []key.Binding{h.yes, h.no}}
	}
	switch m.screen {
	case screenList:
	case screenOutput:
		switch {
		case m.live != nil:
			return keyMap{short: []key.Binding{h.stop, h.scroll, h.running}}
		case m.page != nil && m.page.retry != nil:
			return keyMap{short: []key.Binding{h.retry, h.scroll, h.cont}}
		}
		return keyMap{short: []key.Binding{h.cont, h.scroll}}
	case screenHistory:
		return keyMap{short: []key.Binding{h.entry, h.up, h.down, h.reload, h.leave}}
	case screenDoctor:
		return keyMap{short: []key.Binding{h.recheck, h.scroll, h.leave}}
	case screenReview:
		return keyMap{short: []key.Binding{h.applyReview, h.include, h.reviewed, h.up, h.down, h.leave}}
	case screenDetail:
		d := m.detail
		if d.versions != nil {
			if d.versions.listWidth == m.w() {
				// Too narrow for the notes beside the list.
				return keyMap{short: []key.Binding{h.choose, h.notesTab, h.step, h.notes, h.cancel}}
			}
			return keyMap{short: []key.Binding{h.choose, h.step, h.notes, h.cancel}}
		}
		other := h.info
		switch {
		case len(d.views()) == 1:
			other = key.NewBinding(key.WithDisabled())
		case d.view == viewInfo:
			other = h.readme
		}
		switch {
		case d.plugin != nil:
			if ch, ok := m.checks[d.plugin.PluginID]; ok && ch.Err == nil && ch.Result.Kind == updates.Available {
				return keyMap{short: []key.Binding{h.update, other, h.toggle, h.version, h.remove, h.rollback, h.home, h.scroll, h.back}}
			}
			return keyMap{short: []key.Binding{other, h.toggle, h.update, h.version, h.remove, h.rollback, h.home, h.logs, h.scroll, h.back}}
		case d.change != nil && d.review != nil:
			return keyMap{short: []key.Binding{h.back, other, h.home, h.scroll}}
		case d.change != nil:
			return keyMap{short: []key.Binding{m.applyKey(d), other, h.home, h.scroll, h.back}}
		default:
			return keyMap{short: []key.Binding{h.install, other, h.version, h.home, h.scroll, h.back}}
		}
	}
	if m.filters[m.tab].Focused() {
		return keyMap{short: []key.Binding{h.open, h.move, h.keep, h.clear}}
	}
	nav := []key.Binding{h.up, h.down, h.page, h.ends}
	if m.tab == tabInstalled {
		return keyMap{
			short: []key.Binding{h.details, h.browse, h.toggle, h.update, h.remove, h.filter, h.quit, m.moreKey()},
			full: [][]key.Binding{
				nav,
				{h.details, h.toggle, h.update, h.all, h.check, h.remove, h.rollback},
				{h.filter, h.reload, h.home, h.output, h.history, h.doctor, h.browse, h.less, h.quit},
			},
		}
	}
	return keyMap{
		short: []key.Binding{h.preview, h.instTab, h.search, h.sort, h.quit, m.moreKey()},
		full: [][]key.Binding{
			nav,
			{h.preview, h.sort, h.refresh, h.home},
			{h.search, h.output, h.history, h.doctor, h.instTab, h.less, h.quit},
		},
	}
}

// applyKey is the binding that applies a change preview.
func (m *model) applyKey(d *detail) key.Binding {
	a, verb := changeKey(d)
	if len(m.keys[a]) == 0 {
		return key.NewBinding(key.WithDisabled())
	}
	return key.NewBinding(key.WithKeys(m.keys[a]...), key.WithHelp(m.keys.name(a), verb))
}

func (m *model) moreKey() key.Binding {
	h := m.helpKeys
	if m.showHelp {
		return h.less
	}
	return h.more
}
