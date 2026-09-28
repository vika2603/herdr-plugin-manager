package ui

import (
	"charm.land/bubbles/v2/key"

	"github.com/vika2603/herdr-plugin-manager/internal/updates"
)

// The bindings exist for the help bar only; Update matches key strings
// directly.

func bind(keys, desc string) key.Binding {
	return key.NewBinding(key.WithKeys(keys), key.WithHelp(keys, desc))
}

var (
	keyUp       = bind("↑/k/ctrl+p", "up")
	keyDown     = bind("↓/j/ctrl+n", "down")
	keyPage     = bind("pgup/pgdn/ctrl+b/f", "page")
	keyEnds     = bind("g/G", "top/bottom")
	keyScroll   = bind("↑/↓", "scroll")
	keyBack     = bind("esc", "back")
	keyQuit     = bind("q", "quit")
	keyMore     = bind("?", "more")
	keyLess     = bind("?", "less")
	keyFilter   = bind("/", "filter")
	keySearch   = bind("/", "search")
	keyOutput   = bind("o", "last output")
	keyDetails  = bind("enter", "details")
	keyToggle   = bind("space", "enable/disable")
	keyEnable   = bind("e", "enable/disable")
	keyUpdate   = bind("u", "update")
	keyAll      = bind("U", "update all")
	keyCheck    = bind("c", "check updates")
	keyRemove   = bind("x", "uninstall")
	keyReload   = bind("r", "reload")
	keyLogs     = bind("r", "reload logs")
	keyRefresh  = bind("r", "refresh index")
	keySort     = bind("s", "sort")
	keyPreview  = bind("enter", "preview & install")
	keyInstall  = bind("enter", "install")
	keyApply    = bind("enter", "update")
	keyBrowse   = bind("tab", "marketplace")
	keyInstTab  = bind("tab", "installed")
	keyOpen     = bind("enter", "open")
	keyKeep     = bind("tab", "keep")
	keyClear    = bind("esc", "clear")
	keyMove     = bind("↑/↓/ctrl+n/p", "move")
	keyYes      = bind("y", "confirm")
	keyNo       = bind("n", "cancel")
	keyContinue = bind("any key", "back")
	keyHome     = bind("w", "homepage")
	keyInfo     = bind("tab", "info")
	keyReadme   = bind("tab", "readme")
)

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
	if m.confirm != nil {
		return keyMap{short: []key.Binding{keyYes, keyNo}}
	}
	switch m.screen {
	case screenList:
	case screenOutput:
		return keyMap{short: []key.Binding{keyContinue, keyScroll}}
	case screenDetail:
		d := m.detail
		other := keyInfo
		switch {
		case len(d.views()) == 1:
			other = key.NewBinding(key.WithDisabled())
		case d.view == viewInfo:
			other = keyReadme
		}
		switch {
		case d.plugin != nil:
			if ch, ok := m.checks[d.plugin.PluginID]; ok && ch.Err == nil && ch.Result.Kind == updates.Available {
				return keyMap{short: []key.Binding{keyUpdate, other, keyEnable, keyRemove, keyHome, keyScroll, keyBack}}
			}
			return keyMap{short: []key.Binding{other, keyEnable, keyUpdate, keyRemove, keyHome, keyLogs, keyScroll, keyBack}}
		case d.update != nil:
			return keyMap{short: []key.Binding{keyApply, other, keyHome, keyScroll, keyBack}}
		default:
			return keyMap{short: []key.Binding{keyInstall, other, keyHome, keyScroll, keyBack}}
		}
	}
	if m.filters[m.tab].Focused() {
		return keyMap{short: []key.Binding{keyOpen, keyMove, keyKeep, keyClear}}
	}
	nav := []key.Binding{keyUp, keyDown, keyPage, keyEnds}
	if m.tab == tabInstalled {
		return keyMap{
			short: []key.Binding{keyDetails, keyBrowse, keyToggle, keyUpdate, keyRemove, keyFilter, keyQuit, m.moreKey()},
			full: [][]key.Binding{
				nav,
				{keyDetails, keyToggle, keyUpdate, keyAll, keyCheck, keyRemove},
				{keyFilter, keyReload, keyHome, keyOutput, keyBrowse, keyLess, keyQuit},
			},
		}
	}
	return keyMap{
		short: []key.Binding{keyPreview, keyInstTab, keySearch, keySort, keyQuit, m.moreKey()},
		full: [][]key.Binding{
			nav,
			{keyPreview, keySort, keyRefresh, keyHome},
			{keySearch, keyOutput, keyInstTab, keyLess, keyQuit},
		},
	}
}

func (m *model) moreKey() key.Binding {
	if m.showHelp {
		return keyLess
	}
	return keyMore
}
