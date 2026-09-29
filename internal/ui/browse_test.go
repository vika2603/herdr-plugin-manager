package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/vika2603/herdr-plugin-manager/internal/market"
)

// marketFake is a marketplace with a plugin at a repository root, one deep
// in a repository of several, one for another platform and one without a
// description.
func marketFake() *fakeBackend {
	b := newFake()
	pushed := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	b.index = &market.Index{Repositories: []market.Repository{
		{
			Owner: "kryptamine", Name: "herdr-auto-title", Stars: 42, StarsDelta7d: 5, Language: "Go", PushedAt: pushed,
			CreatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Topics: []string{"herdr-plugin", "tabs", "llm"},
			Manifests: []market.Manifest{{Path: "herdr-plugin.toml", ID: "herdr.auto-title", Name: "Auto Title", Version: "0.9.1",
				MinHerdrVersion: "0.8.2", Platforms: []string{"linux", "macos"}, Description: "Automatically generates contextual tab titles"}},
		},
		{
			Owner: "ogulcancelik", Name: "herdr-plugin-examples", Stars: 30, Language: "TypeScript", PushedAt: pushed.AddDate(0, -4, 0),
			Manifests: []market.Manifest{
				{Path: "agent-telegram-notify/herdr-plugin.toml", ID: "examples.agent-telegram-notify", Name: "Agent Telegram Notify", Version: "0.1.0",
					MinHerdrVersion: "0.9.0", Description: "Send a Telegram message when an agent needs you"},
				{Path: "github-link-preview/herdr-plugin.toml", ID: "examples.github-link-preview", Name: "GitHub Link Preview", Version: "0.1.0"},
			},
		},
		{
			Owner: "someone", Name: "win-tool", Stars: 2,
			Manifests: []market.Manifest{{Path: "herdr-plugin.toml", ID: "someone.win-tool", Name: "Win Tool", Version: "1.0.0",
				Platforms: []string{"windows"}, Description: "Windows only helper"}},
		},
	}}
	return b
}

func startMarket(t *testing.T, width int) *harness {
	t.Helper()
	b := marketFake()
	h := start(t, b)
	h.m.now = func() time.Time { return time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC) }
	h.m.Update(tea.WindowSizeMsg{Width: width, Height: 36})
	h.press("tab")
	return h
}

func TestMarketplaceListLeadsWithWhatAPluginIsFor(t *testing.T) {
	h := startMarket(t, 90)
	out := h.words()
	t.Log("\n" + out)
	for _, want := range []string{
		"Auto Title 0.9.1 ★ 42",
		"Automatically generates contextual tab titles · Go · pushed 3d ago",
		"Send a Telegram message when an agent needs you · repo TypeScript · pushed 4mo ago",
		"Win Tool 1.0.0 ★ 2 ✕ not for macos",
		"GitHub Link Preview 0.1.0 ★ 30",
		"No description",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q", want)
		}
	}
	if strings.Contains(out, "ogulcancelik/herdr-plugin-examples/agent-telegram-notify") {
		t.Errorf("the long source should not take the description's place in the list")
	}
	if strings.Contains(out, "│") {
		t.Errorf("a narrow screen should not split into columns")
	}
}

func TestWideMarketplaceShowsTheSelectedListing(t *testing.T) {
	h := startMarket(t, 140)
	h.press("down")
	out := h.words()
	t.Log("\n" + out)
	for _, want := range []string{
		"│ Agent Telegram Notify 0.1.0 · examples.agent-telegram-notify",
		"STATUS not installed",
		"RUNS ON any platform · herdr ≥ 0.9.0 ✓",
		"VERSION 0.1.0 in the manifest on the default branch",
		"SOURCE ogulcancelik/herdr-plugin-examples/agent-",
		"TOPICS none",
		"REPOSITORY ★ 30 · TypeScript · last push 2026-05-26",
		"2 plugins in it · the plugin is in",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("listing lacks %q", want)
		}
	}
	h.press("down")
	if out := h.words(); !strings.Contains(out, "No description in the manifest or the repository.") {
		t.Errorf("a missing description should be said so:\n%s", out)
	}
	h.press("down")
	out = h.words()
	for _, want := range []string{"language not reported", "last push not", "✕ supports windows, not macos"} {
		if !strings.Contains(out, want) {
			t.Errorf("listing lacks %q:\n%s", want, out)
		}
	}
}

func TestMarketplaceSearchSaysHowToChangeIt(t *testing.T) {
	h := startMarket(t, 120)
	h.press("/", "t", "i", "t", "l", "e", "tab")
	out := h.words()
	if !strings.Contains(out, "/ title · / edits · esc clears") || !strings.Contains(out, "1 of 4 match · by relevance") {
		t.Errorf("a kept search does not say how to edit or clear it:\n%s", out)
	}
	h.press("/", "z", "z", "z")
	if out := h.words(); !strings.Contains(out, "No marketplace plugin matches “titlezzz”. esc clears the search.") {
		t.Errorf("no hint when nothing matches:\n%s", out)
	}
	h.press("esc")
	if out := h.words(); !strings.Contains(out, "/ search the marketplace 4 plugins · by popular") {
		t.Errorf("esc did not clear the search:\n%s", out)
	}
}
