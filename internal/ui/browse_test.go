package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/vika2603/herdr-client/herdr"

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
		{
			Owner: "someone", Name: "mac-tool", Stars: 1,
			Manifests: []market.Manifest{{Path: "herdr-plugin.toml", ID: "someone.mac-tool", Name: "Mac Tool", Version: "1.0.0",
				Platforms: []string{"macos"}, Description: "macOS only helper"}},
		},
	}}
	return b
}

// platforms are the ones the manager supports; each test of what can run
// where runs on both, whichever the tests run on.
var platforms = []string{"linux", "macos"}

func startMarket(t *testing.T, width int, platform string) *harness {
	t.Helper()
	b := marketFake()
	h := start(t, b)
	h.m.now = func() time.Time { return time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC) }
	h.m.platform = platform
	h.m.Update(tea.WindowSizeMsg{Width: width, Height: 36})
	h.press("tab")
	return h
}

func TestMarketplaceListLeadsWithWhatAPluginIsFor(t *testing.T) {
	h := startMarket(t, 90, "macos")
	out := h.words()
	for _, want := range []string{
		"Auto Title 0.9.1 ★ 42",
		"Automatically generates contextual tab titles · Go · pushed 3d ago",
		"Send a Telegram message when an agent needs you · repository pushed 4mo ago",
		"GitHub Link Preview 0.1.0 ★ 30",
		"No description",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "ogulcancelik/herdr-plugin-examples/agent-telegram-notify") {
		t.Errorf("the long source should not take the description's place in the list")
	}
	if strings.Contains(out, "│") {
		t.Errorf("a narrow screen should not split into columns")
	}
	// The language GitHub reports is the whole repository's, which for a
	// plugin in a subdirectory is not the plugin's.
	if strings.Contains(out, "TypeScript") {
		t.Errorf("the list gives a subdirectory plugin its repository's language:\n%s", out)
	}
}

func TestMarketplaceKeepsTheDescriptionWhenRoomIsShort(t *testing.T) {
	h := startMarket(t, 110, "macos")
	out := h.words()
	if !strings.Contains(out, "Send a Telegram message when an agent needs you │") {
		t.Errorf("the description should keep its room, without the meta:\n%s", out)
	}
}

func TestMarketplaceMarksWhatCannotRunHere(t *testing.T) {
	for _, platform := range platforms {
		t.Run(platform, func(t *testing.T) {
			h := startMarket(t, 90, platform)
			out := h.words()
			if !strings.Contains(out, "Win Tool 1.0.0 ★ 2 ✕ not for "+platform) {
				t.Errorf("a Windows plugin is not marked:\n%s", out)
			}
			// Only a macOS plugin is marked on Linux, and nothing that runs
			// here is.
			mac := strings.Contains(out, "Mac Tool 1.0.0 ★ 1 ✕ not for "+platform)
			if mac != (platform != "macos") || strings.Contains(out, "Auto Title 0.9.1 ★ 42 ✕") {
				t.Errorf("marks on %s:\n%s", platform, out)
			}
		})
	}
}

func TestWideMarketplaceShowsTheSelectedListing(t *testing.T) {
	h := startMarket(t, 140, "macos")
	h.press("down")
	out := h.words()
	for _, want := range []string{
		"│ Agent Telegram Notify 0.1.0 · examples.agent-telegram-notify",
		"PLATFORMS any (none declared)", "HERDR ≥ 0.9.0 ✓",
		"VERSION 0.1.0 on the default branch",
		"TOPICS none",
		"REPOSITORY ogulcancelik/herdr-plugin-examples", "FOLDER agent-telegram-notify",
		"STARS ★ 30", "LANGUAGE TypeScript (whole repository)", "LAST PUSH 4 months ago · 2026-05-26", "PLUGINS 2 in this repository",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("listing lacks %q", want)
		}
	}
	if strings.Contains(out, "STATUS") || strings.Contains(out, "GitHub's figures") {
		t.Errorf("a plugin that is not installed needs no status row, and the repository no note:\n%s", out)
	}
	h.press("up")
	if out := h.words(); !strings.Contains(out, "LANGUAGE Go") || strings.Contains(out, "(whole repository)") {
		t.Errorf("a plugin at the repository root shares its language plainly:\n%s", out)
	}
	h.press("down", "down")
	if out := h.words(); !strings.Contains(out, "No description in the manifest or the repository.") {
		t.Errorf("a missing description should be said so:\n%s", out)
	}
	h.press("down")
	out = h.words()
	for _, want := range []string{"LANGUAGE not reported", "LAST PUSH not reported"} {
		if !strings.Contains(out, want) {
			t.Errorf("listing lacks %q:\n%s", want, out)
		}
	}
}

func TestWideListingSaysWhyAPluginCannotRunHere(t *testing.T) {
	for _, platform := range platforms {
		t.Run(platform, func(t *testing.T) {
			h := startMarket(t, 140, platform)
			h.press("down", "down", "down")
			if out := h.words(); !strings.Contains(out, "│ Win Tool") || !strings.Contains(out, "PLATFORMS windows ✕ not "+platform) {
				t.Errorf("the listing does not say why Win Tool cannot run on %s:\n%s", platform, out)
			}
		})
	}
}

func TestMarketplaceSearchSaysHowToChangeIt(t *testing.T) {
	h := startMarket(t, 120, "macos")
	h.press("/", "t", "i", "t", "l", "e", "tab")
	out := h.words()
	if !strings.Contains(out, "/ title · / edits · esc clears") || !strings.Contains(out, "1 of 5 match · by relevance") {
		t.Errorf("a kept search does not say how to edit or clear it:\n%s", out)
	}
	h.press("/", "z", "z", "z")
	if out := h.words(); !strings.Contains(out, "No marketplace plugin matches “titlezzz”. esc clears the search.") {
		t.Errorf("no hint when nothing matches:\n%s", out)
	}
	h.press("esc")
	if out := h.words(); !strings.Contains(out, "/ search the marketplace 5 plugins · by popular") {
		t.Errorf("esc did not clear the search:\n%s", out)
	}
}

func TestMarketplaceTitleKeepsItsMarks(t *testing.T) {
	for _, platform := range platforms {
		t.Run(platform, func(t *testing.T) {
			b := marketFake()
			b.index.Repositories = append(b.index.Repositories, market.Repository{
				Owner: "o", Name: "long", Stars: 12345,
				Manifests: []market.Manifest{{Path: "herdr-plugin.toml", ID: "o.long", Name: "An Extremely Long Plugin Name That Goes On And On",
					Version: "1.0.0-beta.20260929+build.123456", Platforms: []string{"windows"}, Description: "Long"}},
			})
			h := start(t, b)
			h.m.platform = platform
			h.m.Update(tea.WindowSizeMsg{Width: 60, Height: 40})
			h.press("tab", "/", "e", "x", "t", "r", "e", "m", "e", "tab")
			out := h.words()
			if !strings.Contains(out, "✕ not for "+platform) || !strings.Contains(out, "An Extremely") {
				t.Errorf("the mark was pushed off a narrow line:\n%s", out)
			}
		})
	}
}

func TestShortWindowSaysTheListingGoesOn(t *testing.T) {
	b := marketFake()
	topics := []string{"herdr-plugin"}
	for i := range 30 {
		topics = append(topics, fmt.Sprintf("topic-%02d", i))
	}
	b.index.Repositories[0].Topics = topics
	b.index.Repositories[0].Manifests[0].Description = strings.Repeat("A long description that says what the plugin does. ", 12) + "The very end."
	h := start(t, b)
	h.m.platform = "macos"
	h.m.Update(tea.WindowSizeMsg{Width: 140, Height: 20})
	h.press("tab")
	if out := h.words(); !strings.Contains(out, "… Press enter shows the rest.") {
		t.Errorf("a clipped listing does not say so:\n%s", out)
	}
	h.press("enter")
	all := strings.Join(h.m.detailLines(h.m.detail), "\n")
	if !strings.Contains(all, "The very end.") || !strings.Contains(all, "topic-29") {
		t.Errorf("the plugin's screen does not hold the whole description and every topic:\n%s", ansi.Strip(all))
	}
}

func TestMarketplaceTellsAnotherInstallOfTheSameID(t *testing.T) {
	for _, tt := range []struct {
		name      string
		installed herdr.InstalledPluginInfo
		mark      string
		status    string
	}{
		{"linked locally", herdr.InstalledPluginInfo{PluginID: "herdr.auto-title", Name: "Auto Title", Version: "0.9.1-dev", PluginRoot: "/src/auto-title", Enabled: true},
			"◆ linked locally", "STATUS ◆ linked locally from /src/auto-title; unlink it to"},
		{"from a fork", herdr.InstalledPluginInfo{PluginID: "herdr.auto-title", Name: "Auto Title", Version: "0.9.0", Enabled: true,
			Source: herdr.Some(herdr.PluginSourceInfo{Kind: herdr.Some(herdr.PluginSourceKindGithub), Owner: herdr.Some("someone"), Repo: herdr.Some("auto-title-fork"), RequestedRef: herdr.Some("main")})},
			"▲ installed from another source", "STATUS ▲ installed from someone/auto-title-fork@main"},
		{"from this listing", herdr.InstalledPluginInfo{PluginID: "herdr.auto-title", Name: "Auto Title", Version: "0.9.1", Enabled: true,
			Source: herdr.Some(herdr.PluginSourceInfo{Kind: herdr.Some(herdr.PluginSourceKindGithub), Owner: herdr.Some("kryptamine"), Repo: herdr.Some("herdr-auto-title")})},
			"✓ installed", "STATUS ✓ installed 0.9.1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := marketFake()
			b.plugins = []herdr.InstalledPluginInfo{tt.installed}
			h := start(t, b)
			h.m.platform = "macos"
			h.m.Update(tea.WindowSizeMsg{Width: 150, Height: 40})
			h.press("tab")
			out := h.words()
			if !strings.Contains(out, "Auto Title 0.9.1 ★ 42 "+tt.mark) || !strings.Contains(out, tt.status) {
				t.Errorf("want %q and %q:\n%s", tt.mark, tt.status, out)
			}
		})
	}
}

// shownOrders presses the sort key n times on the marketplace and returns
// the order the count line names each time, starting with the one before.
func shownOrders(h *harness, n int) []string {
	name := func() string {
		_, after, ok := strings.Cut(h.words(), "· by ")
		if !ok {
			return ""
		}
		return strings.Fields(after)[0]
	}
	out := []string{name()}
	for range n {
		h.press("s")
		out = append(out, name())
	}
	return out
}

// Without a search relevance sorts as popular, so every press of s must
// still change the order; with one, relevance is part of the cycle.
func TestSortChangesTheOrderOnEveryPress(t *testing.T) {
	h := start(t, newFake())
	h.press("tab")
	if got, want := shownOrders(h, 5), []string{"popular", "trending", "recent", "newest", "name", "popular"}; !slices.Equal(got, want) {
		t.Errorf("without a search, sort shows %q, want %q", got, want)
	}
	h.press("/", "g", "a", "tab")
	if got, want := shownOrders(h, 6), []string{"popular", "trending", "recent", "newest", "name", "relevance", "popular"}; !slices.Equal(got, want) {
		t.Errorf("with a search, sort shows %q, want %q", got, want)
	}
	h.press("/", "esc")
	if got := shownOrders(h, 1); !slices.Equal(got, []string{"popular", "trending"}) {
		t.Errorf("after clearing the search, sort shows %q, want popular then trending", got)
	}
}
