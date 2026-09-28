package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/vika2603/herdr-plugin-manager/internal/config"
)

func TestTypingKeepsTheTextFieldKeys(t *testing.T) {
	km := defaultKeymap()
	for k, want := range map[string]action{
		"ctrl+n": actDown, "down": actDown, "enter": actOpen, "esc": actClose,
		// Typed, or edited by the field.
		"j": "", "q": "", "ctrl+u": "", "space": "",
	} {
		if got := km.typing(k); got != want {
			t.Errorf("typing %q: %q, want %q", k, got, want)
		}
	}
	if km.action("ctrl+u", onList) != actPageUp {
		t.Error("ctrl+u pages up outside the text field")
	}
}

func TestKeymapOverrides(t *testing.T) {
	km, err := newKeymap(map[string][]string{"install": {"I"}, "sort": {}})
	if err != nil {
		t.Fatal(err)
	}
	if km.action("I", onDetail) != actInstall || km.action("i", onDetail) != "" {
		t.Error("an override should replace the default keys")
	}
	if km.name(actSort) != "" {
		t.Error("an empty list should unbind the action")
	}

	if _, err := newKeymap(map[string][]string{"install": {"u"}}); err == nil || !strings.Contains(err.Error(), `"u" is bound to both install and update`) {
		t.Errorf("a key on two actions of one screen: err = %v", err)
	}
	if _, err := newKeymap(map[string][]string{"back": {"q"}}); err == nil {
		t.Error("back and quit share the detail screen")
	}
	// esc is back on a plugin's screen and close on the list, never both.
	if _, err := newKeymap(nil); err != nil {
		t.Errorf("the defaults conflict: %v", err)
	}
	if _, err := newKeymap(map[string][]string{"instal": {"i"}}); err == nil {
		t.Error("an unknown action should be reported")
	}
}

func TestConfiguredKeysDriveTheUI(t *testing.T) {
	b := newFake()
	h := &harness{t: t, m: newModel(context.Background(), b, Options{Config: config.Config{Keys: map[string][]string{"install": {"I"}, "switch": {"]"}}}})}
	h.m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	h.run(h.m.Init())
	if !strings.Contains(h.screen(), "Press ] for the marketplace") {
		t.Errorf("the intro should name the configured key:\n%s", h.screen())
	}
	h.press("]", "enter", "i")
	for _, c := range b.Calls() {
		if strings.HasPrefix(c, "install") {
			t.Fatalf("i still installs: %q", b.Calls())
		}
	}
	h.press("enter")
	if words := strings.Join(strings.Fields(h.screen()), " "); !strings.Contains(words, "Press I to install") || !strings.Contains(words, "I install ] readme") {
		t.Errorf("hints should name the configured key:\n%s", h.screen())
	}
	h.press("I")
	if got := b.Calls(); !strings.HasPrefix(got[len(got)-1], "install carol/gadget") {
		t.Errorf("I should install: %q", got)
	}
}

func TestABadConfigFallsBackToTheDefaults(t *testing.T) {
	h := &harness{t: t, m: newModel(context.Background(), newFake(), Options{Config: config.Config{Keys: map[string][]string{"install": {"u"}}}})}
	h.m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if !strings.Contains(h.screen(), "using the default keys") || h.m.keys.name(actInstall) != "i" {
		t.Errorf("a conflict should be reported and the defaults used:\n%s", h.screen())
	}
}
