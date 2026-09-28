package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/vika2603/herdr-plugin-manager/internal/config"
)

func TestPalettes(t *testing.T) {
	p, err := palettes(config.Theme{})
	if err != nil || p[dark].Accent != accentPresets["indigo"][dark] || p[light].Accent != accentPresets["indigo"][light] {
		t.Errorf("default accent: %v %v", p[dark].Accent, err)
	}
	p, err = palettes(config.Theme{Accent: "teal", Dark: map[string]string{"fg": "#010203"}})
	if err != nil || p[dark].Accent != "#3CC8B4" || p[dark].FG != "#010203" || p[light].FG != basePalettes[light].FG {
		t.Errorf("a preset and a dark override: %+v %v", p[dark], err)
	}
	if p, _ = palettes(config.Theme{Accent: "#123456"}); p[light].Accent != "#123456" || p[dark].Accent != "#123456" {
		t.Errorf("a hex accent should apply to both backgrounds: %v", p)
	}
	for name, bad := range map[string]config.Theme{
		"accent":       {Accent: "purple"},
		"role":         {Light: map[string]string{"background": "#000000"}},
		"colour":       {Dark: map[string]string{"fg": "white"}},
		"short colour": {Dark: map[string]string{"fg": "#fff"}},
	} {
		if p, err := palettes(bad); err == nil || p != defaultPalettes() {
			t.Errorf("bad %s: %v; want an error and the defaults", name, err)
		}
	}
}

func TestMix(t *testing.T) {
	if got := mix("#000000", "#FFFFFF", 0.5); got != "#808080" {
		t.Errorf("mix = %s", got)
	}
	if got := mix("#8B93FF", "#8B93FF", 0.3); got != "#8B93FF" {
		t.Errorf("mixing a colour with itself = %s", got)
	}
}

func TestThemeModeFixesTheBackground(t *testing.T) {
	m := newModel(context.Background(), newFake(), Options{Config: config.Config{Theme: config.Theme{Mode: "light"}}})
	if m.theme.dark {
		t.Fatal("mode light should start light")
	}
	m.Update(tea.BackgroundColorMsg{})
	if m.theme.dark {
		t.Error("a fixed mode should ignore the terminal's background")
	}
	m = newModel(context.Background(), newFake(), Options{Config: config.Config{Theme: config.Theme{Mode: "dim"}}})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if h := (&harness{t: t, m: m}); !strings.Contains(h.screen(), `theme mode "dim"`) {
		t.Error("an unknown mode should be reported")
	}
}
