package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-plugin-manager/internal/config"
	"github.com/vika2603/herdr-plugin-manager/internal/manager"
	"github.com/vika2603/herdr-plugin-manager/internal/source"
	"github.com/vika2603/herdr-plugin-manager/internal/updates"
)

// runAsync starts cmd, an operation, and delivers its message once it ends.
func runAsync(cmd tea.Cmd) <-chan tea.Msg {
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	return done
}

// blockUntilCancelled makes each install print, then wait for its context
// to end. started is closed when the first one waits.
func blockUntilCancelled(b *fakeBackend) (started <-chan struct{}) {
	ch := make(chan struct{})
	var once sync.Once
	b.installHook = func(ctx context.Context) {
		once.Do(func() { close(ch) })
		<-ctx.Done()
	}
	return ch
}

func (h *harness) status() string { return ansi.Strip(h.m.statusLine()) }

func TestOperationOutputShowsAsItArrivesAndCtrlCCancelsIt(t *testing.T) {
	b := newFake()
	started := blockUntilCancelled(b)
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

func TestCtrlCTwiceQuitsWhileCancelling(t *testing.T) {
	b := newFake()
	started := make(chan struct{})
	release := make(chan struct{})
	b.installHook = func(context.Context) {
		close(started)
		<-release
	}
	defer close(release)
	h := start(t, b)
	runAsync(h.m.install(installTarget{src: source.GitHub{Owner: "carol", Repo: "gadget"}}, "carol.gadget", nil))
	<-started
	if _, cmd := h.m.Update(keyMsg("ctrl+c")); quits(cmd) {
		t.Fatal("the first ctrl+c quit")
	}
	if _, cmd := h.m.Update(keyMsg("ctrl+c")); !quits(cmd) {
		t.Error("a second ctrl+c while the operation stops did not quit")
	}
}

func TestCancelledReviewLeavesTheRestUnstartedAndRetryReviewsThem(t *testing.T) {
	b := newFake()
	b.checks["beta"] = updates.Result{Kind: updates.Available, Source: source.GitHub{Owner: "o", Repo: "beta"}, CurrentRef: "v1.0.0", TargetRef: "v1.1.0", TargetCommit: "c2"}
	started := blockUntilCancelled(b)
	h := start(t, b)
	h.press("U")
	if h.m.review == nil || h.m.review.loading() > 0 {
		t.Fatal("the review did not load")
	}
	done := runAsync(h.m.applyReview(h.m.review))
	<-started
	if !strings.Contains(h.words(), "1 of 2: alpha") {
		t.Errorf("the progress is not shown:\n%s", h.words())
	}
	h.m.Update(keyMsg("ctrl+c"))
	_, next := h.m.Update(<-done)
	h.run(next)

	calls := b.Calls()
	if !slices.Contains(calls, "update alpha v1.1.0") || slices.Contains(calls, "update beta v1.1.0") {
		t.Errorf("calls = %q, want alpha started and beta not", calls)
	}
	if out := h.words(); !strings.Contains(out, "beta: not started, cancelled") || !strings.Contains(out, "not started: beta") {
		t.Errorf("the output does not say beta was not started:\n%s", out)
	}
	b.installHook = nil
	h.press("r")
	if h.m.screen != screenReview || len(h.m.review.items) != 2 {
		t.Fatalf("r did not review alpha and beta again: screen %v", h.m.screen)
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
	}{
		{"the first of two", true, 1, []string{"interrupted: alpha", "not started: beta"}, []string{"alpha"}},
		{"the last of two", true, 2, []string{"interrupted: beta"}, []string{"alpha", "beta"}},
		{"the only one", false, 1, []string{"interrupted: alpha"}, []string{"alpha"}},
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
		})
	}
}

func TestNoPluginIsToggledWhileAnOperationRuns(t *testing.T) {
	b := newFake()
	started := blockUntilCancelled(b)
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

func TestDetailShowsBothStreamsOfACommandLog(t *testing.T) {
	b := newFake()
	b.logs = []herdr.PluginCommandLogInfo{{
		LogID: "1", PluginID: "alpha", Command: []string{"sh", "run.sh"}, Status: herdr.PluginCommandStatusFailed,
		ActionID: herdr.Some("go"), Stdout: herdr.Some("step one\nstep two\n"), Stderr: herdr.Some("it broke\n"),
	}}
	h := start(t, b)
	h.press("enter")
	out := h.words()
	for _, want := range []string{"stdout:", "step one", "step two", "stderr:", "it broke"} {
		if !strings.Contains(out, want) {
			t.Errorf("the command log does not show %q:\n%s", want, out)
		}
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

func TestConfigFinding(t *testing.T) {
	dir := t.TempDir()
	if f := ConfigFinding(dir, config.Config{}, nil); f.Health != manager.Healthy || !strings.Contains(f.Summary, "no config file") {
		t.Errorf("no file: %+v", f)
	}
	bad := config.Config{Theme: config.Theme{Mode: "sepia", Accent: "#12"}}
	f := ConfigFinding(dir, bad, errors.New("read x: unknown setting y"))
	if f.Health != manager.Warning || len(f.Details) != 3 {
		t.Errorf("bad config: %+v", f)
	}
	if f := ConfigFinding("", config.Config{}, nil); f.Health != manager.Warning {
		t.Errorf("no directory: %+v", f)
	}
}
