package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-plugin-manager/internal/manager"
	"github.com/vika2603/herdr-plugin-manager/internal/market"
	"github.com/vika2603/herdr-plugin-manager/internal/source"
	"github.com/vika2603/herdr-plugin-manager/internal/updates"
)

// runAsync starts cmd, an operation, and delivers its message once it ends.
func runAsync(cmd tea.Cmd) <-chan tea.Msg {
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	return done
}

func (h *harness) status() string { return ansi.Strip(h.m.statusLine()) }

func TestOperationOutputShowsAsItArrivesAndCtrlCCancelsIt(t *testing.T) {
	b := newFake()
	started := blockCall(b, 1)
	h := start(t, b)
	gadget := installTarget{src: source.GitHub{Owner: "carol", Repo: "gadget"}}
	done := runAsync(h.m.install(gadget, "carol.gadget", nil))
	<-started

	if out := h.words(); h.m.screen != screenOutput || !strings.Contains(out, "(running)") || !strings.Contains(out, "cloning carol/gadget") {
		t.Fatalf("the output screen does not show what herdr printed so far:\n%s", out)
	}
	if !strings.Contains(h.status(), "ctrl+c cancels") {
		t.Errorf("status = %q, want how to cancel", h.status())
	}
	if _, cmd := h.m.Update(keyMsg("ctrl+c")); quits(cmd) {
		t.Fatal("ctrl+c quit instead of cancelling the operation")
	}
	if !strings.Contains(h.status(), "Cancelling") {
		t.Errorf("status = %q, want the operation being cancelled", h.status())
	}
	_, next := h.m.Update(<-done)
	h.run(next)
	if h.m.screen != screenOutput || !strings.Contains(h.words(), "cancelled") {
		t.Errorf("the cancelled operation's output is not shown:\n%s", h.words())
	}
	if h.m.busy != "" || !strings.Contains(h.status(), "cancelled") {
		t.Errorf("busy = %q, status = %q; want it ended as cancelled", h.m.busy, h.status())
	}

	// The manager goes on: the next operation runs, and ctrl+c then quits.
	b.installHook = nil
	h.run(h.m.install(gadget, "carol.gadget", nil))
	if n := len(slices.DeleteFunc(b.Calls(), func(c string) bool { return !strings.HasPrefix(c, "install ") })); n != 2 {
		t.Errorf("installs = %d, want the second one run too", n)
	}
	if h.m.screen != screenList {
		t.Errorf("screen = %v, want the list after the second install succeeded", h.m.screen)
	}
	if _, cmd := h.m.Update(keyMsg("ctrl+c")); !quits(cmd) {
		t.Error("ctrl+c with nothing running did not quit")
	}
}

func TestRetryPreviewsAFailedChangeAgain(t *testing.T) {
	b := newFake()
	b.installErr = errors.New("build failed")
	h := start(t, b)
	h.press("u", "u")
	if h.m.screen != screenOutput || !strings.Contains(h.status(), "r retries") {
		t.Fatalf("screen %v, status %q; want the failure with its retry key", h.m.screen, h.status())
	}
	h.press("r")
	if h.m.screen != screenDetail || !strings.Contains(h.screen(), "Update alpha") {
		t.Fatalf("r did not preview the update again:\n%s", h.screen())
	}
	if got := b.Calls(); got[len(got)-1] != `preview o/alpha "c1"` {
		t.Errorf("calls = %q, want the commit that failed previewed again", got)
	}
}

func TestHistoryListsChangesAndShowsOneInFull(t *testing.T) {
	b := newFake()
	log := filepath.Join(t.TempDir(), "x.log")
	if err := os.WriteFile(log, []byte("herdr said: build failed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := &manager.State{Version: "1.0.0", Source: "o/alpha", Ref: "v1.0.0", Commit: "c0", Enabled: true}
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.Local)
	b.history = []manager.Entry{
		{ID: "1", Time: at, Kind: manager.KindInstall, Plugin: "beta", After: before},
		{ID: "2", Time: at.Add(time.Minute), Kind: manager.KindUpdate, Plugin: "alpha", Before: before, After: before,
			Target: &manager.TargetRecord{Source: "o/alpha", Ref: "v1.1.0", Commit: "c1"},
			Error:  "herdr plugin install exited with status 1", Log: log},
	}
	h := start(t, b)
	h.press("H")
	out := h.words()
	if h.m.screen != screenHistory {
		t.Fatalf("H did not open the history:\n%s", out)
	}
	failed, done := strings.Index(out, "alpha update ✕ failed"), strings.Index(out, "beta install ✓ done")
	if failed < 0 || done < 0 || failed > done {
		t.Errorf("the history does not list the newest change first with its result:\n%s", out)
	}
	if !strings.Contains(out, "exited with status 1") {
		t.Errorf("the failed change does not say why:\n%s", out)
	}

	h.press("enter")
	out = h.words()
	if h.m.screen != screenOutput || !strings.Contains(out, "target: o/alpha") || !strings.Contains(out, "herdr said: build failed") {
		t.Fatalf("enter did not show the change with herdr's output:\n%s", out)
	}
	h.press("esc")
	if h.m.screen != screenHistory {
		t.Fatalf("back from a change returned to %v, not the history", h.m.screen)
	}
	h.press("enter", "r")
	if h.m.screen != screenDetail || !strings.Contains(h.screen(), "Update alpha") {
		t.Fatalf("r did not preview the failed update again:\n%s", h.screen())
	}
}

// blockCall makes the n-th install, counted from 1, print and then wait for
// its context to end. started is closed when it waits.
func blockCall(b *fakeBackend, n int) (started <-chan struct{}) {
	ch := make(chan struct{})
	var calls int
	b.installHook = func(ctx context.Context) {
		calls++
		if calls == n {
			close(ch)
			<-ctx.Done()
		}
	}
	return ch
}

func TestCancellingAnyUpdateOfAReviewIsReportedAsCancelled(t *testing.T) {
	tests := []struct {
		name    string
		both    bool
		block   int
		want    []string
		applied []string
		// retried is how many updates r reviews again.
		retried int
	}{
		{"the first of two", true, 1, []string{"interrupted: alpha", "not started: beta"}, []string{"alpha"}, 2},
		{"the last of two", true, 2, []string{"interrupted: beta"}, []string{"alpha", "beta"}, 1},
		{"the only one", false, 1, []string{"interrupted: alpha"}, []string{"alpha"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newFake()
			if tt.both {
				b.checks["beta"] = updates.Result{Kind: updates.Available, Source: source.GitHub{Owner: "o", Repo: "beta"}, CurrentRef: "v1.0.0", TargetRef: "v1.1.0", TargetCommit: "c2"}
			}
			started := blockCall(b, tt.block)
			h := start(t, b)
			h.press("U")
			done := runAsync(h.m.applyReview(h.m.review))
			<-started
			h.m.Update(keyMsg("ctrl+c"))
			_, next := h.m.Update(<-done)
			h.run(next)

			out := h.words()
			for _, w := range tt.want {
				if !strings.Contains(out, w) {
					t.Errorf("the output does not say %q:\n%s", w, out)
				}
			}
			if strings.Contains(out, "not updated") || strings.Contains(out, "failed") {
				t.Errorf("the cancellation is reported as a failure:\n%s", out)
			}
			if !strings.Contains(h.status(), "cancelled") {
				t.Errorf("status = %q, want cancelled", h.status())
			}
			var applied []string
			for _, c := range b.Calls() {
				if id, ok := strings.CutPrefix(c, "update "); ok {
					applied = append(applied, strings.Fields(id)[0])
				}
			}
			if !slices.Equal(applied, tt.applied) {
				t.Errorf("started updates = %q, want %q", applied, tt.applied)
			}
			h.press("r")
			if h.m.screen != screenReview || len(h.m.review.items) != tt.retried {
				t.Errorf("r did not review the %d updates left again: screen %v", tt.retried, h.m.screen)
			}
		})
	}
}

func TestNoPluginIsToggledWhileAnOperationRuns(t *testing.T) {
	b := newFake()
	started := blockCall(b, 1)
	h := start(t, b)
	done := runAsync(h.m.install(installTarget{src: source.GitHub{Owner: "o", Repo: "alpha"}}, "alpha", nil))
	<-started
	h.press("esc")
	if h.m.screen != screenList {
		t.Fatalf("esc did not leave the running operation's output: screen %v", h.m.screen)
	}
	h.press("space")
	if slices.ContainsFunc(b.Calls(), func(c string) bool { return strings.HasPrefix(c, "set-enabled") }) {
		t.Errorf("a plugin was toggled while the install ran: %q", b.Calls())
	}
	if !strings.Contains(h.status(), "enable or disable plugins once it ends") {
		t.Errorf("status = %q, want why the toggle waits", h.status())
	}
	h.m.Update(keyMsg("ctrl+c"))
	_, next := h.m.Update(<-done)
	h.run(next)
	b.installHook = nil
	h.press("esc", "space")
	if !slices.Contains(b.Calls(), "set-enabled alpha false") {
		t.Errorf("the toggle after the operation did not run: %q", b.Calls())
	}
}

func TestNoOperationStartsWhileAToggleIsUnanswered(t *testing.T) {
	b := newFake()
	h := start(t, b)
	h.m.toggling = 1
	h.press("u", "u")
	if slices.Contains(b.Calls(), "update alpha v1.1.0") {
		t.Error("an update started while a toggle was unanswered")
	}
	if !strings.Contains(h.status(), "still being enabled or disabled") {
		t.Errorf("status = %q, want why the update waits", h.status())
	}
}

func TestLiveOutputAddsLinesAsTheyEndAndWrapsLongOnes(t *testing.T) {
	m := newModel(context.Background(), newFake(), Options{})
	m.width, m.height, m.screen = 40, 12, screenOutput
	m.busy, m.live, m.outputFollow = "Installing x", &liveOp{started: time.Now()}, true
	view := func() string { return ansi.Strip(m.View().Content) }

	_, _ = m.live.Write([]byte("first\nsecond, still being prin"))
	if out := view(); !strings.Contains(out, "first") || !strings.Contains(out, "second, still being prin") {
		t.Fatalf("the output so far is not shown:\n%s", out)
	}
	_, _ = m.live.Write([]byte("ted\n" + strings.Repeat("long ", 20) + "end\n"))
	out := view()
	if !strings.Contains(out, "second, still being printed") || strings.Contains(out, "prin\n") {
		t.Errorf("the line finished after a frame is not shown whole:\n%s", out)
	}
	if !strings.Contains(out, "end") || strings.Count(out, "long") != 20 {
		t.Errorf("the long line is not wrapped onto the screen:\n%s", out)
	}
	for line := range strings.SplitSeq(out, "\n") {
		if ansi.StringWidth(line) > m.width {
			t.Errorf("line wider than the screen: %q", line)
		}
	}
	m.width = 80
	if out := view(); strings.Count(out, "long") != 20 || !strings.Contains(out, "first") {
		t.Errorf("the output is not wrapped again for the new width:\n%s", out)
	}
}

func TestInstallOpensThePluginSayingHowToUseIt(t *testing.T) {
	b := newFake()
	gadget := plugin("carol.gadget", true)
	gadget.Name = "Gadget"
	gadget.Actions = herdr.Some([]herdr.PluginManifestAction{{ID: "go", Title: "Go"}, {ID: "stop", Title: "Stop"}})
	b.bound = map[string][]string{"carol.gadget.stop": {"prefix+s"}}
	b.installHook = func(context.Context) { b.plugins = append(slices.Clone(b.plugins), gadget) }
	h := start(t, b)
	h.press("tab", "enter", "i")
	out := h.words()
	if h.m.screen != screenDetail || h.m.detail.plugin == nil || h.m.detail.plugin.PluginID != "carol.gadget" {
		t.Fatalf("the installed plugin's details did not open:\n%s", out)
	}
	for _, want := range []string{
		"USE", "CONFIG /config/carol.gadget",
		"carol.gadget.go · no key bound", "carol.gadget.stop · prefix+s",
		"BIND A KEY in /herdr/config.toml, then herdr server reload-config",
		`command = "carol.gadget.go"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the details do not say %q:\n%s", want, out)
		}
	}
}

func TestDiagnosticsShowWhatTheDoctorFound(t *testing.T) {
	b := newFake()
	b.findings = []manager.Finding{
		{Area: "herdr", Health: manager.Healthy, Summary: "version 0.9.1 at /bin/herdr"},
		{Area: "server", Health: manager.Warning, Summary: "no herdr server answers", Details: []string{"without it, plugins cannot be enabled"}},
		{Area: "git", Health: manager.Failing, Summary: "not found"},
	}
	h := start(t, b)
	h.m.opts.ConfigDir = t.TempDir()
	h.m.opts.Config.Keys = map[string][]string{"nope": {"x"}}
	h.press("D")
	out := h.words()
	for _, want := range []string{
		"✓ herdr version 0.9.1 at /bin/herdr", "▲ server no herdr server answers", "without it, plugins cannot be enabled",
		"✕ git not found", `▲ hpm config`, `unknown key action "nope"`,
		"1 failing, 2 with warnings, of 4 checked",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the diagnostics do not show %q:\n%s", want, out)
		}
	}
	h.press("r")
	if n := len(slices.DeleteFunc(b.Calls(), func(c string) bool { return c != "doctor" })); n != 2 {
		t.Errorf("r ran the doctor %d times in all, want 2", n)
	}
	h.press("esc")
	if h.m.screen != screenList {
		t.Errorf("esc left the screen at %v", h.m.screen)
	}
}

func TestFiltersChoosePluginsByState(t *testing.T) {
	b := newFake()
	h := start(t, b)
	shows := func(ids ...string) {
		t.Helper()
		var got []string
		for _, p := range h.m.visibleInstalled() {
			got = append(got, p.PluginID)
		}
		if !slices.Equal(got, ids) {
			t.Errorf("filter %q shows %q, want %q", h.m.filters[tabInstalled].Value(), got, ids)
		}
	}
	for query, want := range map[string][]string{
		"is:update": {"alpha"}, "-is:enabled": {"beta"}, "is:current is:disabled": {"beta"}, "alp is:enabled": {"alpha"},
	} {
		h.m.filters[tabInstalled].SetValue(query)
		shows(want...)
	}
	h.m.filters[tabInstalled].SetValue("is:outdated")
	if !strings.Contains(h.status(), "unknown filter is:outdated; is: takes enabled, disabled, update") {
		t.Errorf("status = %q, want the states is: takes", h.status())
	}

	b.plugins = append(b.plugins, plugin("carol.gadget", true))
	h.run(h.m.loadInstalled())
	h.m.tab = tabBrowse
	h.m.filters[tabBrowse].SetValue("is:installed")
	if got := h.m.visibleEntries(); len(got) != 1 || got[0].Manifest.ID != "carol.gadget" {
		t.Errorf("is:installed shows %d listings", len(got))
	}
	h.m.filters[tabBrowse].SetValue("-is:installed")
	if got := h.m.visibleEntries(); len(got) != 0 {
		t.Errorf("-is:installed shows %d listings, want none", len(got))
	}
}

// A restore or rollback that only enabled or disabled a plugin records no
// target; retrying it sets the state again rather than taking it for an
// uninstall.
func TestRetryOfAnEnabledStateChangeSetsTheStateAgain(t *testing.T) {
	b := newFake()
	disabled := &manager.State{Version: "1.0.0", Source: "o/beta", Ref: "v1.0.0", Commit: "c0"}
	b.history = []manager.Entry{{ID: "1", Time: time.Now(), Kind: manager.KindRestore, Plugin: "beta",
		Before: disabled, After: disabled, Error: "herdr server is not running"}}
	h := start(t, b)
	h.press("H", "enter")
	if out := h.words(); strings.Contains(out, "uninstall") || !strings.Contains(out, "r retry") {
		t.Fatalf("the entry does not offer to set the state again:\n%s", out)
	}
	h.press("r")
	if !slices.Contains(b.Calls(), "set-enabled beta true") {
		t.Errorf("calls = %q, want beta enabled again", b.Calls())
	}
}

// Too narrow for the notes beside the version list, tab shows them in its
// place, and ? lists every key the short help bar has no room for.
func TestNarrowScreenReachesNotesAndEveryKey(t *testing.T) {
	b := newFake()
	b.preview.Ref, b.preview.DefaultBranch = "v1.1.0", "main"
	b.preview.Releases = []string{"v1.1.0", "v1.0.0"}
	var notes strings.Builder
	notes.WriteString("Gadgets start twice as fast.\n")
	for i := range 40 {
		fmt.Fprintf(&notes, "\n- point %d", i+1)
	}
	b.releases = []market.Release{{Tag: "v1.1.0", Name: "Faster gadgets", Notes: notes.String(), URL: "https://example/v1.1.0"}}
	h := start(t, b)
	h.m.Update(tea.WindowSizeMsg{Width: 44, Height: 30})
	h.press("tab", "enter", "v")
	if out := h.words(); strings.Contains(out, "twice as fast") || !strings.Contains(out, "tab shows the release notes") {
		t.Fatalf("a narrow picker should show the list and how to reach the notes:\n%s", out)
	}
	h.press("tab")
	if out := h.words(); !strings.Contains(out, "Faster gadgets") || !strings.Contains(out, "twice as fast") || !strings.Contains(out, "tab shows the versions") {
		t.Errorf("tab did not show the notes of v1.1.0, and how to return to the versions:\n%s", out)
	}
	h.press("ctrl+d", "ctrl+d", "ctrl+d")
	if out := h.words(); !strings.Contains(out, "point 40") || !strings.Contains(out, "https://example/v1.1.0") {
		t.Errorf("the notes do not scroll to their end:\n%s", out)
	}
	h.m.Update(tea.WindowSizeMsg{Width: 70, Height: 30})
	h.screen()
	if out := h.words(); !strings.Contains(out, "scroll notes") {
		t.Errorf("reading the notes, the bar does not say how to page them:\n%s", out)
	}
	h.press("esc", "?")
	out := h.words()
	for _, want := range []string{"i install", "v version", "w homepage", "? less"} {
		if !strings.Contains(out, want) {
			t.Errorf("? does not list %q on a narrow preview:\n%s", want, out)
		}
	}
}

// Back retraces the way in: a preview opened from the version picker
// returns to the picker as it was left, a preview opened from a plugin's
// details returns to them, and the list keeps its search, order and
// selection.
func TestBackRetracesTheWayIn(t *testing.T) {
	b := newFake()
	b.preview.Ref, b.preview.DefaultBranch = "v1.1.0", "main"
	b.preview.Releases = []string{"v1.1.0", "v1.0.0"}
	h := start(t, b)
	h.press("tab", "/", "g", "a", "d", "tab", "s")
	h.press("enter", "v", "down", "enter")
	if d := h.m.detail; d == nil || d.versions != nil || d.install.ref != "v1.0.0" {
		t.Fatalf("choosing v1.0.0 did not preview it:\n%s", h.screen())
	}
	h.press("esc")
	if vp := h.m.detail.versions; vp == nil || vp.rows[vp.cursor].ref != "v1.0.0" {
		t.Fatalf("back from the preview did not return to the picker at v1.0.0:\n%s", h.screen())
	}
	h.press("up", "enter", "esc")
	if vp := h.m.detail.versions; vp == nil || vp.rows[vp.cursor].ref != "v1.1.0" {
		t.Fatalf("back from the version already shown did not return to the picker:\n%s", h.screen())
	}
	h.press("esc", "esc")
	if h.m.screen != screenList || h.m.filters[tabBrowse].Value() != "gad" || h.m.order != market.ByPopular {
		t.Errorf("back at the list, search %q and order %v are not kept", h.m.filters[tabBrowse].Value(), h.m.order)
	}

	h.press("1", "enter", "u", "esc")
	if h.m.screen != screenDetail || h.m.detail.plugin == nil {
		t.Errorf("back from an update opened in the details did not return to them:\n%s", h.screen())
	}
	h.press("esc", "u", "esc")
	if h.m.screen != screenList {
		t.Errorf("back from an update opened in the list did not return to it:\n%s", h.screen())
	}

	b.installErr = errors.New("build failed")
	h.press("u", "u", "x")
	if h.m.screen != screenOutput {
		t.Fatalf("a key other than back left the output")
	}
	h.press("esc")
	if h.m.screen != screenList {
		t.Errorf("back did not leave the output")
	}
}

// A README read for a preview arrives after the version picker has opened a
// preview of another version: it is there when back returns to the first,
// and the preview of the version already shown reads its own.
func TestReadmeArrivingAfterLeavingItsPreviewIsShownOnReturn(t *testing.T) {
	b := newFake()
	b.preview.Ref, b.preview.DefaultBranch = "v1.1.0", "main"
	b.preview.Releases = []string{"v1.1.0", "v1.0.0"}
	h := start(t, b)
	h.press("tab", "enter")
	_, readme := h.m.Update(keyMsg("tab"))
	h.press("v", "down", "enter")
	h.run(readme)
	h.press("esc", "esc", "tab")
	if out := h.screen(); !strings.Contains(out, "Does gadget things.") {
		t.Errorf("the README read before the version was chosen is not shown on return:\n%s", out)
	}

	h = start(t, b)
	h.press("tab", "enter")
	_, readme = h.m.Update(keyMsg("tab"))
	h.press("v", "enter")
	h.run(readme)
	h.press("tab")
	if out := h.screen(); !strings.Contains(out, "Does gadget things.") {
		t.Errorf("the preview of the version already shown is stuck reading its README:\n%s", out)
	}

	// beta has no README; that is found while its update preview is open
	// over the details left on the README view.
	b.checks["beta"] = updates.Result{Kind: updates.Available, Source: source.GitHub{Owner: "o", Repo: "beta"}, CurrentRef: "v1.0.0", TargetRef: "v1.1.0", TargetCommit: "c2"}
	h = start(t, b)
	h.press("down")
	_, open := h.m.Update(keyMsg("enter"))
	h.press("tab", "u")
	h.run(open)
	h.press("esc")
	if d := h.m.detail; d == nil || d.plugin == nil || d.view != viewInfo {
		t.Errorf("back in the details of a plugin without a README, they are not on Info:\n%s", h.screen())
	}
}
