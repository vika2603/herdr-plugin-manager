package ui

import (
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
	version, choose, cancel, notes, step key.Binding
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
		details: first(actOpen, "details"), toggle: first(actToggle, "enable/disable"),
		update: first(actUpdate, "update"), all: first(actUpdateAll, "update all"), check: first(actCheck, "check updates"),
		remove: first(actUninstall, "uninstall"), reload: first(actReload, "reload"), logs: first(actReload, "reload logs"),
		refresh: first(actReload, "refresh index"), sort: first(actSort, "sort"), install: first(actInstall, "install"),
		apply: first(actUpdate, "update"), browse: first(actSwitch, "marketplace"), instTab: first(actSwitch, "installed"),
		open: first(actOpen, "open"), keep: first(actSwitch, "keep"), clear: first(actClose, "clear"), move: move,
		yes: first(actConfirm, "confirm"), no: fixed("any other key", "cancel"), cont: fixed("any key", "back"),
		home: first(actHomepage, "homepage"), info: first(actSwitch, "info"), readme: first(actSwitch, "readme"),
		version: first(actVersion, "version"), choose: first(actOpen, "choose"), cancel: first(actBack, "cancel"),
		notes: pair(actPageUp, actPageDown, "scroll notes"), step: pair(actUp, actDown, "move"),
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

func (m *model) keyMap() keyMap {
	h := m.helpKeys
	if m.confirm != nil {
		return keyMap{short: []key.Binding{h.yes, h.no}}
	}
	switch m.screen {
	case screenList:
	case screenOutput:
		return keyMap{short: []key.Binding{h.cont, h.scroll}}
	case screenDetail:
		d := m.detail
		if d.versions != nil {
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
				return keyMap{short: []key.Binding{h.update, other, h.toggle, h.remove, h.home, h.scroll, h.back}}
			}
			return keyMap{short: []key.Binding{other, h.toggle, h.update, h.remove, h.home, h.logs, h.scroll, h.back}}
		case d.update != nil:
			return keyMap{short: []key.Binding{h.apply, other, h.home, h.scroll, h.back}}
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
				{h.details, h.toggle, h.update, h.all, h.check, h.remove},
				{h.filter, h.reload, h.home, h.output, h.browse, h.less, h.quit},
			},
		}
	}
	return keyMap{
		short: []key.Binding{h.install, h.details, h.instTab, h.search, h.sort, h.quit, m.moreKey()},
		full: [][]key.Binding{
			nav,
			{h.install, h.details, h.sort, h.refresh, h.home},
			{h.search, h.output, h.instTab, h.less, h.quit},
		},
	}
}

func (m *model) moreKey() key.Binding {
	h := m.helpKeys
	if m.showHelp {
		return h.less
	}
	return h.more
}
