package ui

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// action is something a key does. Its name is the key of the [keys] table in
// the config file.
type action string

const (
	actUp             action = "up"
	actDown           action = "down"
	actPageUp         action = "page_up"
	actPageDown       action = "page_down"
	actTop            action = "top"
	actBottom         action = "bottom"
	actOpen           action = "open"
	actSwitch         action = "switch"
	actBack           action = "back"
	actClose          action = "close"
	actQuit           action = "quit"
	actSearch         action = "search"
	actHelp           action = "help"
	actHomepage       action = "homepage"
	actOutput         action = "output"
	actReload         action = "reload"
	actCheck          action = "check"
	actInstall        action = "install"
	actUpdate         action = "update"
	actUpdateAll      action = "update_all"
	actToggle         action = "toggle"
	actUninstall      action = "uninstall"
	actSort           action = "sort"
	actVersion        action = "version"
	actConfirm        action = "confirm"
	actTabInstalled   action = "tab_installed"
	actTabMarketplace action = "tab_marketplace"
)

// screenKind groups where an action applies; two actions of one kind cannot
// share a key.
type screenKind int

const (
	onList screenKind = iota
	onDetail
	onPicker
	onOutput
	onDialog
)

// defaultKeys are the keys of each action, and the screens it applies on.
var defaultKeys = map[action]struct {
	keys    []string
	screens []screenKind
}{
	actUp:       {[]string{"up", "k", "ctrl+p"}, []screenKind{onList, onDetail, onPicker, onOutput}},
	actDown:     {[]string{"down", "j", "ctrl+n"}, []screenKind{onList, onDetail, onPicker, onOutput}},
	actPageUp:   {[]string{"pgup", "ctrl+b", "ctrl+u"}, []screenKind{onList, onDetail, onPicker, onOutput}},
	actPageDown: {[]string{"pgdown", "ctrl+f", "ctrl+d"}, []screenKind{onList, onDetail, onPicker, onOutput}},
	actTop:      {[]string{"home", "g"}, []screenKind{onList, onDetail}},
	actBottom:   {[]string{"end", "G"}, []screenKind{onList}},
	actOpen:     {[]string{"enter"}, []screenKind{onList, onDetail, onPicker}},
	actSwitch:   {[]string{"tab", "shift+tab"}, []screenKind{onList, onDetail}},
	actBack:     {[]string{"esc", "backspace", "left", "h"}, []screenKind{onDetail, onPicker, onOutput}},
	actClose:    {[]string{"esc"}, []screenKind{onList}},
	actQuit:     {[]string{"q"}, []screenKind{onList, onDetail, onPicker}},
	actSearch:   {[]string{"/"}, []screenKind{onList}},
	actHelp:     {[]string{"?"}, []screenKind{onList}},
	actHomepage: {[]string{"w"}, []screenKind{onList, onDetail}},
	actOutput:   {[]string{"o"}, []screenKind{onList}},
	actReload:   {[]string{"r"}, []screenKind{onList, onDetail}},
	actCheck:    {[]string{"c"}, []screenKind{onList}},
	// Install only from a preview, so no key in the list installs, however
	// often it is pressed.
	actInstall:        {[]string{"i"}, []screenKind{onDetail}},
	actUpdate:         {[]string{"u"}, []screenKind{onList, onDetail}},
	actUpdateAll:      {[]string{"U"}, []screenKind{onList}},
	actToggle:         {[]string{"space", "e"}, []screenKind{onList, onDetail}},
	actUninstall:      {[]string{"x", "delete"}, []screenKind{onList, onDetail}},
	actSort:           {[]string{"s"}, []screenKind{onList}},
	actVersion:        {[]string{"v"}, []screenKind{onDetail, onPicker}},
	actConfirm:        {[]string{"y", "Y"}, []screenKind{onDialog}},
	actTabInstalled:   {[]string{"1"}, []screenKind{onList}},
	actTabMarketplace: {[]string{"2"}, []screenKind{onList}},
}

// typingActions are the ones a focused search field leaves to the list.
var typingActions = []action{actUp, actDown, actPageUp, actPageDown, actOpen, actSwitch, actClose}

// editingKeys are what a text field does itself, so they are never taken
// from it while typing, whatever they are bound to.
var editingKeys = []string{
	"space", "backspace", "delete", "left", "right", "home", "end",
	"ctrl+a", "ctrl+b", "ctrl+d", "ctrl+e", "ctrl+f", "ctrl+h", "ctrl+k", "ctrl+u", "ctrl+w",
}

// Key is an action and the keys that do it, as `hpm keys` lists them.
type Key struct {
	Action string
	Keys   []string
}

// Keys lists every action with its keys after overrides, by action name.
// With an error, the defaults are listed.
func Keys(overrides map[string][]string) ([]Key, error) {
	km, err := newKeymap(overrides)
	out := make([]Key, 0, len(km))
	for _, a := range km.sorted() {
		out = append(out, Key{Action: string(a), Keys: km[a]})
	}
	return out, err
}

// keymap is the keys of every action.
type keymap map[action][]string

// newKeymap applies overrides, from the config file, to the default keys.
// An override replaces an action's keys; an empty list unbinds it.
func newKeymap(overrides map[string][]string) (keymap, error) {
	km := keymap{}
	for a, d := range defaultKeys {
		km[a] = d.keys
	}
	for name, keys := range overrides {
		a := action(name)
		if _, ok := defaultKeys[a]; !ok {
			return defaultKeymap(), fmt.Errorf("config: unknown key action %q", name)
		}
		km[a] = keys
	}
	if err := km.check(); err != nil {
		return defaultKeymap(), err
	}
	return km, nil
}

func defaultKeymap() keymap {
	km, _ := newKeymap(nil)
	return km
}

// check reports a key bound to two actions on the same screen.
func (km keymap) check() error {
	var conflicts []string
	for s := onList; s <= onDialog; s++ {
		owner := map[string]action{}
		for _, a := range km.sorted() {
			if !slices.Contains(defaultKeys[a].screens, s) {
				continue
			}
			for _, k := range km[a] {
				if other, ok := owner[k]; ok && other != a {
					conflicts = append(conflicts, fmt.Sprintf("%q is bound to both %s and %s", k, other, a))
				}
				owner[k] = a
			}
		}
	}
	if len(conflicts) > 0 {
		slices.Sort(conflicts)
		return fmt.Errorf("config: %s", strings.Join(slices.Compact(conflicts), "; "))
	}
	return nil
}

func (km keymap) sorted() []action {
	out := make([]action, 0, len(km))
	for a := range km {
		out = append(out, a)
	}
	slices.Sort(out)
	return out
}

// is reports whether key k does a.
func (km keymap) is(k string, a action) bool {
	return slices.Contains(km[a], k)
}

// typing is the action k does while a search field has focus, or "" when
// the field takes k.
func (km keymap) typing(k string) action {
	if slices.Contains(editingKeys, k) || utf8.RuneCountInString(k) == 1 {
		return ""
	}
	for _, a := range typingActions {
		if km.is(k, a) {
			return a
		}
	}
	return ""
}

// action is what k does on screens of kind s, or "".
func (km keymap) action(k string, s screenKind) action {
	for a, keys := range km {
		if slices.Contains(keys, k) && slices.Contains(defaultKeys[a].screens, s) {
			return a
		}
	}
	return ""
}

// name is how the first key of a is shown, for hints; "" when a is unbound.
func (km keymap) name(a action) string {
	if len(km[a]) == 0 {
		return ""
	}
	return keyLabel(km[a][0])
}

// label is every key of a, shown as the help bar shows them.
func (km keymap) label(a action) string {
	names := make([]string, len(km[a]))
	for i, k := range km[a] {
		names[i] = keyLabel(k)
	}
	return strings.Join(names, "/")
}

func keyLabel(k string) string {
	switch k {
	case "up":
		return "↑"
	case "down":
		return "↓"
	case "pgdown":
		return "pgdn"
	}
	return k
}
