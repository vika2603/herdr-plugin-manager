package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/vika2603/herdr-client/herdr"
	"github.com/vika2603/herdr-client/plugin/manifest"

	"github.com/vika2603/herdr-plugin-manager/internal/manager"
	"github.com/vika2603/herdr-plugin-manager/internal/market"
	"github.com/vika2603/herdr-plugin-manager/internal/source"
	"github.com/vika2603/herdr-plugin-manager/internal/updates"
)

// fakeBackend records the mutating calls the model makes.
type fakeBackend struct {
	mu         sync.Mutex
	plugins    []herdr.InstalledPluginInfo
	checks     map[string]updates.Result
	checkErrs  map[string]error
	index      *market.Index
	preview    *manager.Preview
	installErr error
	calls      []string
	// lastUpdated is the plugin record the last update was given.
	lastUpdated herdr.InstalledPluginInfo
	// installHook, when set, runs inside an install with its context.
	installHook func(context.Context)
	// undo is what PlanRollback returns, with undoErr.
	undo    manager.Undo
	undoErr error
	// versions is what Versions returns, and explain what Explain does.
	versions manager.Versions
	explain  *manager.Explanation
	// previewGate, when set, keeps Preview reading its plugin list until it
	// is closed.
	previewGate chan struct{}
	// releases are what Releases returns.
	releases []market.Release
	// history is what HistoryEntries returns, the oldest first.
	history []manager.Entry
	// bound are the keys Usage reports bound, by action command, and
	// findings what Doctor finds.
	bound    map[string][]string
	findings []manager.Finding
}

func (f *fakeBackend) record(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf(format, args...))
}

func (f *fakeBackend) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeBackend) Installed(context.Context) ([]herdr.InstalledPluginInfo, error) {
	return f.plugins, nil
}

func (*fakeBackend) HerdrVersion(context.Context) string { return "0.9.1" }

func (f *fakeBackend) SetEnabled(_ context.Context, id string, enabled bool) error {
	f.record("set-enabled %s %v", id, enabled)
	return nil
}

func (*fakeBackend) Logs(context.Context, string, int) ([]herdr.PluginCommandLogInfo, error) {
	return []herdr.PluginCommandLogInfo{}, nil
}

// Apply records an install as "install <source> <ref>", and any other
// change as "<kind> <id> <ref>".
func (f *fakeBackend) Apply(ctx context.Context, c manager.Change, out io.Writer) manager.Outcome {
	t := c.Target
	if c.Kind == manager.KindInstall {
		f.record("install %s %q", t.Source, t.Ref)
	} else {
		f.record("%s %s %s", c.Kind, c.ID, t.Ref)
	}
	if c.Current != nil {
		f.mu.Lock()
		f.lastUpdated = *c.Current
		f.mu.Unlock()
	}
	fmt.Fprintln(out, "cloning", t.Source)
	if f.installHook != nil {
		f.installHook(ctx)
	}
	o := manager.Outcome{Kind: c.Kind, ID: c.ID, Err: f.installErr}
	if ctx.Err() != nil {
		o.Err = fmt.Errorf("%w: herdr was interrupted", manager.ErrCancelled)
	}
	if c.Current != nil {
		before := manager.StateOf(*c.Current)
		o.Before = &before
	}
	after := manager.State{Version: "new", Source: t.Source.String(), Ref: t.Ref, Commit: t.Commit, Enabled: true}
	if o.Err != nil {
		o.After = o.Before
	} else {
		o.After = &after
	}
	return o
}

func (f *fakeBackend) Explain(_ context.Context, p herdr.InstalledPluginInfo, preview *manager.Preview) manager.Explanation {
	if f.explain != nil {
		return *f.explain
	}
	return manager.Explanation{Headline: "Changes to " + p.PluginID, From: p.Version, To: preview.Manifest.Version}
}

func (f *fakeBackend) Review(ctx context.Context, ch manager.Checked, herdrVersion string) manager.Review {
	r := manager.Review{Checked: ch}
	res := ch.Result
	r.Preview, r.Err = f.PreviewAt(ctx, res.Source, res.TargetRef, res.TargetCommit, herdrVersion, nil)
	if r.Err == nil {
		r.Preview.RequireID(ch.Plugin.PluginID)
		r.Explain = f.Explain(ctx, ch.Plugin, r.Preview)
	}
	return r
}

func (f *fakeBackend) Versions(_ context.Context, src source.GitHub) (manager.Versions, error) {
	f.record("versions %s", src)
	return f.versions, nil
}

func (f *fakeBackend) PreviewAt(ctx context.Context, src source.GitHub, ref, commit, herdrVersion string, installed []herdr.InstalledPluginInfo) (*manager.Preview, error) {
	p, err := f.Preview(ctx, src, commit, "", herdrVersion, installed)
	if err == nil {
		p.Ref, p.Commit = ref, commit
	}
	return p, err
}

func (f *fakeBackend) PlanRollback(_ context.Context, id string) (manager.Undo, error) {
	f.record("plan-rollback %s", id)
	return f.undo, f.undoErr
}

func (f *fakeBackend) Rollback(_ context.Context, u manager.Undo, _ io.Writer) manager.Outcome {
	f.record("rollback %s %s", u.Entry.Plugin, u.Target.Ref)
	return manager.Outcome{Kind: manager.KindRollback, ID: u.Entry.Plugin, Before: u.Entry.After, After: u.Entry.Before}
}

func (f *fakeBackend) RemoteReadme(_ context.Context, src source.GitHub, ref string) (*manager.Readme, error) {
	f.record("readme %s %q", src, ref)
	return &manager.Readme{Markdown: "# Gadget\n\nDoes **gadget** things.", Location: "https://example/README.md"}, nil
}

func (f *fakeBackend) Releases(_ context.Context, src source.GitHub) ([]market.Release, error) {
	f.record("releases %s", src)
	return f.releases, nil
}

func (*fakeBackend) InstalledReadme(p herdr.InstalledPluginInfo) (*manager.Readme, error) {
	if p.PluginID == "beta" {
		return nil, manager.ErrNoReadme
	}
	return &manager.Readme{Markdown: "# " + p.Name + "\n\nThe installed README.", Location: "/plugins/" + p.PluginID + "/README.md"}, nil
}

func (f *fakeBackend) OpenURL(_ context.Context, url string) error {
	f.record("open %s", url)
	return nil
}

func (f *fakeBackend) Usage(_ context.Context, p herdr.InstalledPluginInfo) manager.Usage {
	u := manager.Usage{ConfigDir: "/config/" + p.PluginID, HerdrConfig: "/herdr/config.toml"}
	for _, a := range p.Actions.ValueOrZero() {
		ua := manager.UsageAction{ID: a.ID, Title: a.Title, Command: p.PluginID + "." + a.ID}
		ua.Keys = f.bound[ua.Command]
		u.Actions = append(u.Actions, ua)
	}
	return u
}

func (f *fakeBackend) Doctor(context.Context, string) []manager.Finding {
	f.record("doctor")
	return f.findings
}

func (f *fakeBackend) HistoryEntries() ([]manager.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.history), nil
}

func (f *fakeBackend) Uninstall(_ context.Context, id string, _ io.Writer) error {
	f.record("uninstall %s", id)
	return nil
}

func (f *fakeBackend) CheckAll(_ context.Context, plugins []herdr.InstalledPluginInfo) []manager.Checked {
	out := make([]manager.Checked, len(plugins))
	for i, p := range plugins {
		out[i] = manager.Checked{Plugin: p, Result: f.checks[p.PluginID], Err: f.checkErrs[p.PluginID]}
	}
	return out
}

func (f *fakeBackend) Index(context.Context, bool) (*market.Index, market.Status, error) {
	if f.index == nil {
		return nil, market.Status{}, errors.New("offline")
	}
	return f.index, market.Status{FetchedAt: time.Now()}, nil
}

// Preview returns the configured preview; for the installed fixtures, whose
// sources are o/<id>, its manifest carries that id as a real one would.
func (f *fakeBackend) Preview(_ context.Context, src source.GitHub, ref, hint, _ string, installed []herdr.InstalledPluginInfo) (*manager.Preview, error) {
	if hint != "" {
		f.record("preview %s %q hint %s", src, ref, hint)
	} else {
		f.record("preview %s %q", src, ref)
	}
	// Read the list the way manager.Preview does; with a gate, keep reading
	// until it closes, so a concurrent write is seen by the race detector.
	for {
		for _, p := range installed {
			_ = p.Enabled
		}
		if f.previewGate == nil {
			break
		}
		select {
		case <-f.previewGate:
		default:
			continue
		}
		break
	}
	p := *f.preview
	if ref != "" {
		p.Ref = ref
	}
	if src.Owner == "o" {
		mf := *p.Manifest
		mf.ID = src.Repo
		p.Manifest = &mf
	}
	p.Problems = slices.Clone(p.Problems)
	return &p, nil
}

func plugin(id string, enabled bool) herdr.InstalledPluginInfo {
	return herdr.InstalledPluginInfo{
		PluginID: id, Name: id, Version: "1.0.0", Enabled: enabled,
		Source: herdr.Some(herdr.PluginSourceInfo{
			Kind:  herdr.Some(herdr.PluginSourceKindGithub),
			Owner: herdr.Some("o"),
			Repo:  herdr.Some(id),
		}),
	}
}

func newFake() *fakeBackend {
	return &fakeBackend{
		plugins: []herdr.InstalledPluginInfo{plugin("alpha", true), plugin("beta", false)},
		checks: map[string]updates.Result{
			"alpha": {Kind: updates.Available, Source: source.GitHub{Owner: "o", Repo: "alpha"}, CurrentRef: "v1.0.0", TargetRef: "v1.1.0", TargetCommit: "c1"},
			"beta":  {Kind: updates.UpToDate},
		},
		index: &market.Index{Repositories: []market.Repository{{
			Owner: "carol", Name: "gadget", Stars: 3, HeadCommit: "e0e0",
			Manifests: []market.Manifest{{Path: "herdr-plugin.toml", ID: "carol.gadget", Name: "Gadget", Version: "0.1.0"}},
		}}},
		preview: &manager.Preview{
			Source:   source.GitHub{Owner: "carol", Repo: "gadget"},
			Manifest: &manifest.Manifest{ID: "carol.gadget", Name: "Gadget", Version: "0.1.0", MinHerdrVersion: "0.9.0"},
		},
	}
}

// harness drives the model the way the program would, running each command
// and feeding its message back. Spinner ticks and commands that do not
// return promptly, such as cursor blinks, are dropped.
type harness struct {
	t *testing.T
	m *model
}

func start(t *testing.T, b Backend) *harness {
	t.Helper()
	h := &harness{t: t, m: newModel(context.Background(), b, Options{})}
	h.m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	h.run(h.m.Init())
	return h
}

func (h *harness) run(cmd tea.Cmd) {
	h.t.Helper()
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		cmd, queue = queue[0], queue[1:]
		if cmd == nil {
			continue
		}
		msg, ok := runCmd(cmd)
		if !ok {
			continue
		}
		switch msg := msg.(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case spinner.TickMsg:
		default:
			_, next := h.m.Update(msg)
			queue = append(queue, next)
		}
	}
}

func runCmd(cmd tea.Cmd) (tea.Msg, bool) {
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		return msg, true
	case <-time.After(200 * time.Millisecond):
		return nil, false
	}
}

func (h *harness) press(keys ...string) {
	h.t.Helper()
	for _, k := range keys {
		_, cmd := h.m.Update(keyMsg(k))
		h.run(cmd)
	}
}

func keyMsg(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "ctrl+n", "ctrl+p", "ctrl+f", "ctrl+b", "ctrl+d", "ctrl+u":
		return tea.KeyPressMsg{Code: rune(k[len(k)-1]), Mod: tea.ModCtrl}
	case "space":
		return tea.KeyPressMsg{Code: ' ', Text: " "}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	r, _ := utf8.DecodeRuneInString(k)
	return tea.KeyPressMsg{Code: r, Text: k}
}

func (h *harness) screen() string {
	return ansi.Strip(h.m.View().Content)
}

// words is the screen with every run of spaces made one, so a check does not
// depend on how many columns separate two words.
func (h *harness) words() string {
	lines := strings.Split(h.screen(), "\n")
	for i, l := range lines {
		lines[i] = strings.Join(strings.Fields(l), " ")
	}
	return strings.Join(lines, "\n")
}

func TestListShowsPluginsAndUpdates(t *testing.T) {
	h := start(t, newFake())
	out := h.words()
	for _, want := range []string{"Installed 2", "Marketplace 1", "alpha", "↑ v1.1.0", "beta", "↑ 1 update"} {
		if !strings.Contains(out, want) {
			t.Errorf("screen lacks %q:\n%s", want, out)
		}
	}
}

func TestStatusLineCountsFailedChecks(t *testing.T) {
	down := errors.New("could not reach GitHub")
	for _, tt := range []struct {
		name      string
		checkErrs map[string]error
		upToDate  bool
		want      string
	}{
		{"all fail", map[string]error{"alpha": down, "beta": down}, false, "✕ 2 checks failed · c to retry"},
		{"one fails", map[string]error{"beta": down}, false, "↑ 1 update · u to review · ✕ 1 check failed"},
		{"one fails with no update", map[string]error{"beta": down}, true, "✕ 1 check failed · c to retry"},
		{"none fail", nil, true, "All GitHub plugins are up to date"},
		{"none fail with an update", nil, false, "↑ 1 update · u to review"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := newFake()
			b.checkErrs = tt.checkErrs
			if tt.upToDate {
				b.checks["alpha"] = updates.Result{Kind: updates.UpToDate}
			}
			h := start(t, b)
			if got := strings.TrimSpace(ansi.Strip(h.m.statusLine())); got != tt.want {
				t.Errorf("status line = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStatusLineNeedsEveryPluginChecked(t *testing.T) {
	b := newFake()
	b.checks["alpha"] = updates.Result{Kind: updates.UpToDate}
	h := start(t, b)
	delete(h.m.checks, "beta")
	if got := strings.TrimSpace(ansi.Strip(h.m.statusLine())); got != "Updates not checked · c to check" {
		t.Errorf("with beta not checked, the status line = %q", got)
	}
}

func TestUpdateAllExplainsWhyNothingRuns(t *testing.T) {
	down := errors.New("could not reach GitHub")
	for _, tt := range []struct {
		name  string
		setup func(*fakeBackend)
		after func(*model)
		want  string
	}{
		{"all checks fail", func(b *fakeBackend) { b.checkErrs = map[string]error{"alpha": down, "beta": down} }, nil,
			"✕ No updates to apply: 2 checks failed"},
		{"a check fails and the rest are up to date", func(b *fakeBackend) { b.checkErrs = map[string]error{"beta": down} }, nil,
			"✕ No updates to apply: 1 check failed"},
		{"a plugin is not checked", nil, func(m *model) { delete(m.checks, "beta") },
			"✕ No updates to apply: 1 plugin not checked"},
		{"a check is running", nil, func(m *model) { m.checking = true },
			"Checking for updates…"},
		{"every check succeeds", nil, nil,
			"✓ No updates available"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := newFake()
			b.checks["alpha"] = updates.Result{Kind: updates.UpToDate}
			if tt.setup != nil {
				tt.setup(b)
			}
			h := start(t, b)
			if tt.after != nil {
				tt.after(h.m)
			}
			h.press("U")
			if got := ansi.Strip(h.m.statusLine()); !strings.Contains(got, tt.want) {
				t.Errorf("after U, the status line = %q, want %q", got, tt.want)
			}
			if h.m.confirm != nil || len(b.Calls()) != 0 {
				t.Errorf("U with nothing to update asked %v or ran %q", h.m.confirm, b.Calls())
			}
		})
	}
}

func TestUpdateAllSkipsFailedChecks(t *testing.T) {
	b := newFake()
	b.checkErrs = map[string]error{"beta": errors.New("could not reach GitHub")}
	h := start(t, b)
	h.press("U", "u")
	if h.m.confirm == nil || !strings.Contains(h.m.confirm.prompt, "Update 1 plugin?") {
		t.Fatalf("the review did not ask to update alpha: %+v", h.m.confirm)
	}
	h.press("y")
	if got := b.Calls(); !slices.Contains(got, "update alpha v1.1.0") || slices.ContainsFunc(got, func(c string) bool { return strings.HasPrefix(c, "update beta") }) {
		t.Errorf("calls = %q, want alpha updated and beta left alone", got)
	}
}

func TestToggleEnabled(t *testing.T) {
	b := newFake()
	h := start(t, b)
	h.press("down", "space")
	if got := b.Calls(); len(got) != 1 || got[0] != "set-enabled beta true" {
		t.Fatalf("calls = %q", got)
	}
	if !h.m.installed[1].Enabled {
		t.Error("list not updated after enabling")
	}
}

func TestUninstallNeedsConfirmation(t *testing.T) {
	b := newFake()
	h := start(t, b)
	h.press("x")
	if !strings.Contains(h.screen(), "Uninstall alpha?") {
		t.Fatalf("no confirmation shown:\n%s", h.screen())
	}
	h.press("n")
	if len(b.Calls()) != 0 {
		t.Fatalf("declined uninstall ran: %q", b.Calls())
	}
	h.press("x", "y")
	if got := b.Calls(); len(got) != 1 || got[0] != "uninstall alpha" {
		t.Fatalf("calls = %q", got)
	}
}

func TestUpdateShowsPreviewFirst(t *testing.T) {
	b := newFake()
	h := start(t, b)
	h.press("u")
	if got := b.Calls(); len(got) != 1 || got[0] != `preview o/alpha "c1"` {
		t.Fatalf("calls before confirming = %q", got)
	}
	if !strings.Contains(h.screen(), "Installed › Update alpha") {
		t.Fatalf("update preview not shown:\n%s", h.screen())
	}
	h.press("u")
	if got := b.Calls(); len(got) != 2 || got[1] != "update alpha v1.1.0" {
		t.Fatalf("calls = %q", got)
	}
	if h.m.screen != screenList {
		t.Error("a successful update should return to the list")
	}
}

func TestUpdateAll(t *testing.T) {
	b := newFake()
	h := start(t, b)
	h.press("U", "u", "y")
	want := []string{`preview o/alpha "c1"`, "update alpha v1.1.0"}
	if got := b.Calls(); !slices.Equal(got, want) {
		t.Fatalf("calls = %q, want %q", got, want)
	}
}

func TestUpdateAllSkipsWhatCannotRunHere(t *testing.T) {
	b := newFake()
	b.preview.Problems = []string{"requires herdr 9.9.9, running 0.9.1"}
	h := start(t, b)
	h.press("U")
	if out := h.words(); !strings.Contains(out, "alpha 1.0.0 ✕ cannot update") || !strings.Contains(out, "requires herdr 9.9.9, running 0.9.1") {
		t.Errorf("the review does not explain why alpha cannot update:\n%s", out)
	}
	h.press("space", "u")
	for _, c := range b.Calls() {
		if strings.HasPrefix(c, "update") {
			t.Fatalf("updated despite problems: %q", b.Calls())
		}
	}
	if h.m.confirm != nil || !strings.Contains(h.words(), "No update is included") {
		t.Errorf("nothing can be applied, yet:\n%s", h.words())
	}
}

// twoUpdates is newFake with updates for both alpha and beta.
func twoUpdates() *fakeBackend {
	b := newFake()
	b.checks["beta"] = updates.Result{Kind: updates.Available, Source: source.GitHub{Owner: "o", Repo: "beta"}, TargetRef: "v2.0.0", TargetCommit: "c2"}
	return b
}

func TestReviewLeavesOutWhatTheUserExcludes(t *testing.T) {
	b := twoUpdates()
	b.explain = &manager.Explanation{Headline: "New release v1.1.0, replacing v1.0.0", Runs: []string{"+ build: make"}}
	h := start(t, b)
	h.press("U")
	out := h.words()
	for _, want := range []string{"Installed › Review 2 updates", "alpha 1.0.0 ● included", "New release v1.1.0, replacing v1.0.0 · what it runs changes", "2 of 2 included · u updates them"} {
		if !strings.Contains(out, want) {
			t.Errorf("review lacks %q:\n%s", want, out)
		}
	}
	h.press("down", "space")
	if out := h.words(); !strings.Contains(out, "beta 1.0.0 ○ left out") || !strings.Contains(out, "1 of 2 included") {
		t.Fatalf("beta was not left out:\n%s", out)
	}
	h.press("u", "y")
	got := b.Calls()
	if !slices.Contains(got, "update alpha v1.1.0") || slices.ContainsFunc(got, func(c string) bool { return strings.HasPrefix(c, "update beta") }) {
		t.Errorf("calls = %q, want alpha updated and beta left out", got)
	}
	if h.m.screen != screenList || !strings.Contains(h.words(), "Updated 1 plugin") {
		t.Errorf("after the review:\n%s", h.words())
	}
	h.press("o")
	if out := h.words(); !strings.Contains(out, "== beta: left out") || !strings.Contains(out, "== alpha (1 of 1)") {
		t.Errorf("the output does not account for each update:\n%s", out)
	}
}

func TestReviewOpensEachUpdateInFull(t *testing.T) {
	b := twoUpdates()
	h := start(t, b)
	h.press("U", "enter")
	if h.m.screen != screenDetail || !strings.Contains(h.words(), "Review › Update alpha") || !strings.Contains(h.words(), "esc back") {
		t.Fatalf("enter did not show alpha's update:\n%s", h.words())
	}
	h.press("u")
	if slices.ContainsFunc(b.Calls(), func(c string) bool { return strings.HasPrefix(c, "update") }) {
		t.Fatalf("u in a reviewed update applied it alone: %q", b.Calls())
	}
	h.press("esc")
	if h.m.screen != screenReview {
		t.Errorf("esc did not return to the review: %v", h.m.screen)
	}
	h.press("esc")
	if h.m.screen != screenList || h.m.review != nil {
		t.Errorf("esc did not leave the review")
	}
}

func TestUpdateUsesCurrentEnabledState(t *testing.T) {
	b := newFake()
	h := start(t, b)
	// alpha was enabled when its update was checked; disable it, then update.
	h.press("space", "u", "u")
	if got := b.Calls(); len(got) < 3 || got[0] != "set-enabled alpha false" || !strings.HasPrefix(got[2], "update alpha") {
		t.Fatalf("calls = %q", got)
	}
	if b.lastUpdated.Enabled {
		t.Error("the update was given the stale enabled state, so a disabled plugin would be re-enabled")
	}
}

func TestBrowseInstall(t *testing.T) {
	b := newFake()
	h := start(t, b)
	h.press("tab")
	if !strings.Contains(h.words(), "Gadget 0.1.0") {
		t.Fatalf("browse tab lacks the entry:\n%s", h.screen())
	}
	h.press("enter")
	if !strings.Contains(h.words(), "Gadget 0.1.0 · carol.gadget") {
		t.Fatalf("the preview details should open first:\n%s", h.screen())
	}
	for _, c := range b.Calls() {
		if strings.HasPrefix(c, "readme") {
			t.Fatalf("the README was downloaded before it was asked for: %q", b.Calls())
		}
	}
	h.press("tab")
	if out := h.screen(); !strings.Contains(out, "Does gadget things.") {
		t.Fatalf("tab should show the README:\n%s", out)
	}
	h.press("i")
	if got := b.Calls(); len(got) != 3 || got[2] != `install carol/gadget ""` {
		t.Fatalf("calls = %q", got)
	}
	if !strings.Contains(h.screen(), "carol.gadget is installed: new at the default branch, enabled") {
		t.Errorf("status lacks success:\n%s", h.screen())
	}
}

func TestInstallDetailShowsTheListingWhileThePreviewLoads(t *testing.T) {
	b := newFake()
	h := start(t, b)
	h.press("tab")
	_, cmd := h.m.Update(keyMsg("enter"))
	out := h.words()
	for _, want := range []string{"Gadget 0.1.0 · carol.gadget", "SOURCE carol/gadget"} {
		if !strings.Contains(out, want) {
			t.Errorf("before the preview arrives the detail lacks %q:\n%s", want, out)
		}
	}
	h.run(cmd)
	if got := b.Calls(); len(got) != 1 || got[0] != `preview carol/gadget "" hint e0e0` {
		t.Errorf("calls = %q, want the preview to start from the index's head commit", got)
	}
}

func TestSearchPreviewsATypedSourceOutsideTheMarketplace(t *testing.T) {
	b := newFake()
	h := start(t, b)
	h.press("tab", "/")
	for _, r := range "dave/tool/plugin" {
		h.press(string(r))
	}
	if !strings.Contains(h.screen(), "Press enter to preview dave/tool/plugin from GitHub") {
		t.Fatalf("no hint for a source outside the marketplace:\n%s", h.screen())
	}
	h.press("enter")
	if got := b.Calls(); len(got) != 1 || got[0] != `preview dave/tool/plugin ""` {
		t.Errorf("calls = %q, want a preview of the typed source", got)
	}

	b = newFake()
	h = start(t, b)
	h.press("tab", "/")
	for _, r := range "carol/gadget" {
		h.press(string(r))
	}
	h.press("enter")
	if got := b.Calls(); len(got) != 1 || !strings.HasPrefix(got[0], `preview carol/gadget "" hint`) {
		t.Errorf("calls = %q, want the listed plugin opened from its listing", got)
	}
}

func TestVersionPickerReloadsAndInstallsTheChosenRef(t *testing.T) {
	b := newFake()
	b.preview.Ref, b.preview.DefaultBranch = "v1.1.0", "main"
	b.preview.Releases = []string{"v2.0.0-rc.1", "v1.1.0", "v1.0.0"}
	h := start(t, b)
	h.press("tab", "enter", "v")
	out := h.screen()
	for _, want := range [][]string{{"VERSIONS"}, {"v2.0.0-rc.1", "pre-release"}, {"v1.1.0", "latest", "shown"}, {"default branch (main)"}} {
		if !slices.ContainsFunc(strings.Split(out, "\n"), func(line string) bool {
			return !slices.ContainsFunc(want, func(w string) bool { return !strings.Contains(line, w) })
		}) {
			t.Errorf("picker has no line with %q:\n%s", want, out)
		}
	}
	h.press("down", "enter")
	if got := b.Calls(); got[len(got)-1] != `preview carol/gadget "v1.0.0"` {
		t.Fatalf("calls = %q, want the preview reloaded at v1.0.0", got)
	}
	h.press("i")
	if got := b.Calls(); got[len(got)-1] != `install carol/gadget "v1.0.0"` {
		t.Errorf("calls = %q, want v1.0.0 installed", got)
	}
}

func TestPreviewWithProblemsDoesNotInstall(t *testing.T) {
	b := newFake()
	b.preview.Problems = []string{"requires herdr 9.9.9, running 0.9.1"}
	h := start(t, b)
	h.press("tab", "enter", "i")
	for _, c := range b.Calls() {
		if strings.HasPrefix(c, "install") {
			t.Fatalf("installed despite problems: %q", b.Calls())
		}
	}
	if !strings.Contains(h.screen(), "cannot be installed here") {
		t.Errorf("no explanation shown:\n%s", h.screen())
	}
}

func TestFailedInstallShowsOutput(t *testing.T) {
	b := newFake()
	b.installErr = errors.New("build failed")
	h := start(t, b)
	h.press("tab", "enter", "i")
	if h.m.screen != screenOutput {
		t.Fatalf("screen = %v, want output", h.m.screen)
	}
	out := h.screen()
	if !strings.Contains(out, "cloning carol/gadget") || !strings.Contains(out, "build failed") {
		t.Errorf("output screen lacks herdr's output or the error:\n%s", out)
	}
}

func TestFailedUpdateSaysWhereThePluginStands(t *testing.T) {
	b := newFake()
	b.installErr = errors.New("build failed")
	h := start(t, b)
	h.press("u", "u")
	out := h.words()
	if h.m.screen != screenOutput || !strings.Contains(out, "build failed") || !strings.Contains(out, "alpha is unchanged: 1.0.0 at the default branch, enabled") {
		t.Errorf("the output screen does not say alpha is unchanged:\n%s", out)
	}
}

// withCommit is newFake with alpha installed from the default branch at a
// commit, and releases to choose from.
func withCommit() *fakeBackend {
	b := newFake()
	src := b.plugins[0].Source.ValueOrZero()
	src.ResolvedCommit = herdr.Some(strings.Repeat("a", 40))
	b.plugins[0].Source = herdr.Some(src)
	b.versions = manager.Versions{Releases: []string{"v1.1.0", "v1.0.0"}, DefaultBranch: "main"}
	return b
}

func TestInstalledVersions(t *testing.T) {
	t.Run("the detail says how the plugin is updated", func(t *testing.T) {
		h := start(t, withCommit())
		h.press("enter")
		if out := h.words(); !strings.Contains(out, "UPDATES follows the default branch · v for versions") {
			t.Errorf("detail:\n%s", out)
		}
	})
	t.Run("switch to an older release", func(t *testing.T) {
		b := withCommit()
		h := start(t, b)
		h.press("enter", "v")
		out := h.words()
		for _, want := range []string{"v1.1.0 latest", "default branch (main) · installed", "Reinstall the installed version", "Pin to the installed commit"} {
			if !strings.Contains(out, want) {
				t.Errorf("picker lacks %q:\n%s", want, out)
			}
		}
		h.press("up", "enter")
		out = h.words()
		if !strings.Contains(out, "Switch alpha") || !strings.Contains(out, "UPDATES follows new releases, installed at v1.0.0") || !strings.Contains(out, "i switch") {
			t.Fatalf("no switch preview:\n%s", out)
		}
		h.press("i")
		if got := b.Calls(); got[len(got)-1] != "switch alpha v1.0.0" {
			t.Errorf("calls = %q", got)
		}
	})
	t.Run("pin", func(t *testing.T) {
		b := withCommit()
		h := start(t, b)
		h.press("enter", "v", "down", "down", "enter")
		if out := h.words(); !strings.Contains(out, "Pin alpha") || !strings.Contains(out, "UPDATES pinned to commit aaaaaaaaaaaa") {
			t.Fatalf("no pin preview:\n%s", out)
		}
		h.press("i")
		if got := b.Calls(); got[len(got)-1] != "pin alpha "+strings.Repeat("a", 40) {
			t.Errorf("calls = %q", got)
		}
	})
	t.Run("reinstall at the installed version", func(t *testing.T) {
		b := withCommit()
		h := start(t, b)
		h.press("enter", "v", "enter")
		if out := h.words(); !strings.Contains(out, "Reinstall alpha") {
			t.Fatalf("choosing the installed version should preview a reinstall:\n%s", out)
		}
	})
}

func TestUpdatePreviewSaysWhatChangesInWords(t *testing.T) {
	installed, next := "bb74c548c3a9"+strings.Repeat("0", 28), "d3ba3c61147d"+strings.Repeat("0", 28)
	b := newFake()
	p := plugin("alpha", true)
	p.Name, p.Version = "Auto Title", "0.5.0"
	src := p.Source.ValueOrZero()
	src.ResolvedCommit = herdr.Some(installed)
	p.Source = herdr.Some(src)
	b.plugins = []herdr.InstalledPluginInfo{p}
	b.checks = map[string]updates.Result{"alpha": {
		Kind: updates.Available, Source: source.GitHub{Owner: "o", Repo: "alpha"}, CurrentCommit: installed, TargetCommit: next,
	}}
	b.explain = &manager.Explanation{
		Headline: "New commits on the default branch; the version number stays 0.5.0",
		From:     "0.5.0 at default branch (bb74c548c3a9)", To: "0.5.0 at default branch (d3ba3c61147d)",
		Commits: &market.Comparison{Status: "ahead", Total: 2, Commits: []market.Commit{{Title: "Name tabs after the running command", Author: "Ann"}, {Title: "Fix a crash on empty panes", Author: "Bo"}}},
		Runs:    []string{"+ build: go build ./cmd/auto-title"},
	}
	h := start(t, b)
	h.press("u")
	out := h.words()
	t.Log("\n" + out)
	for _, want := range []string{
		"Installed › Update Auto Title herdr",
		"New commits on the default branch; the version number stays 0.5.0",
		"FROM 0.5.0 at default branch (bb74c548c3a9)", "TO 0.5.0 at default branch (d3ba3c61147d)",
		"COMMITS 2 new", "Name tabs after the running command (Ann)",
		"MANIFEST CHANGES", "+ build: go build ./cmd/auto-title",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("preview lacks %q", want)
		}
	}
	if strings.Count(out, "d3ba3c6") != 1 || strings.Count(out, "bb74c54") != 1 {
		t.Errorf("each commit should be shown once:\n%s", out)
	}
}

func TestRollback(t *testing.T) {
	before := manager.State{Version: "0.9.0", Source: "o/alpha", Ref: "v0.9.0", Commit: "c0", Enabled: true}
	after := manager.State{Version: "1.0.0", Source: "o/alpha", Ref: "v1.0.0", Commit: "c1", Enabled: true}
	undo := manager.Undo{
		Entry:  manager.Entry{Kind: manager.KindUpdate, Plugin: "alpha", Before: &before, After: &after},
		Target: manager.Target{Source: source.GitHub{Owner: "o", Repo: "alpha"}, Ref: "v0.9.0", Commit: "c0"},
	}
	t.Run("previews the earlier version", func(t *testing.T) {
		b := newFake()
		b.undo = undo
		h := start(t, b)
		h.press("z")
		out := h.words()
		if !strings.Contains(out, "Roll back alpha") || !strings.Contains(out, "undo the update of alpha") || !strings.Contains(out, "i roll back") {
			t.Fatalf("no rollback preview:\n%s", out)
		}
		if got := b.Calls(); !slices.Equal(got, []string{"plan-rollback alpha", `preview o/alpha "c0"`}) {
			t.Fatalf("calls = %q", got)
		}
		h.press("i")
		if got := b.Calls(); got[len(got)-1] != "rollback alpha v0.9.0" {
			t.Fatalf("calls = %q", got)
		}
		if !strings.Contains(h.words(), "alpha: 1.0.0 at v1.0.0 (c1), enabled -> 0.9.0 at v0.9.0 (c0), enabled") {
			t.Errorf("the status does not say where alpha stands:\n%s", h.words())
		}
	})
	t.Run("an install is undone by uninstalling after asking", func(t *testing.T) {
		b := newFake()
		b.undo = manager.Undo{Entry: manager.Entry{Kind: manager.KindInstall, Plugin: "alpha", After: &after}, Remove: true}
		h := start(t, b)
		h.press("z")
		if h.m.confirm == nil || !strings.Contains(h.m.confirm.prompt, "Uninstall alpha? That undoes its install") {
			t.Fatalf("confirm = %+v", h.m.confirm)
		}
		h.press("y")
		if got := b.Calls(); got[len(got)-1] != "uninstall alpha" {
			t.Errorf("calls = %q", got)
		}
	})
	t.Run("nothing to undo", func(t *testing.T) {
		b := newFake()
		b.undoErr = fmt.Errorf("%w: no change to alpha is recorded", manager.ErrNothingToUndo)
		h := start(t, b)
		h.press("z")
		if h.m.screen != screenList || !strings.Contains(h.words(), "✕ nothing to roll back: no change to alpha is recorded") {
			t.Errorf("screen:\n%s", h.words())
		}
	})
}

func TestFilterNarrowsList(t *testing.T) {
	h := start(t, newFake())
	h.press("/", "b", "e", "tab")
	out := h.screen()
	if strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Errorf("filter did not narrow the list:\n%s", out)
	}
	h.press("esc")
	if !strings.Contains(h.screen(), "alpha") {
		t.Error("esc did not clear the filter")
	}
}

func TestQuitWaitsForOperation(t *testing.T) {
	h := start(t, newFake())
	h.m.busy = "Installing x"
	if _, cmd := h.m.Update(keyMsg("q")); quits(cmd) {
		t.Fatal("q quit while an operation was running")
	}
	if _, cmd := h.m.Update(keyMsg("ctrl+c")); !quits(cmd) {
		t.Fatal("ctrl+c did not quit")
	}
}

// quits reports whether cmd, or a command it batches, ends the program.
// Commands that do not return promptly, such as timers, are skipped.
func quits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	msg, ok := runCmd(cmd)
	switch msg := msg.(type) {
	case tea.QuitMsg:
		return ok
	case tea.BatchMsg:
		return slices.ContainsFunc(msg, quits)
	}
	return false
}

func TestStatusClearsItself(t *testing.T) {
	h := start(t, newFake())
	h.press("w")
	gen := h.m.statusGen
	if h.m.status == "" {
		t.Fatal("w set no status")
	}
	h.m.setStatus("newer", false)
	h.m.Update(statusExpiredMsg{gen})
	if h.m.status != "newer" {
		t.Errorf("an older timer cleared a newer status: %q", h.m.status)
	}
	h.m.Update(statusExpiredMsg{h.m.statusGen})
	if h.m.status != "" {
		t.Errorf("the status outlived its timer: %q", h.m.status)
	}
}

func TestSelfIsProtectedBeforeConfirming(t *testing.T) {
	b := newFake()
	h := &harness{t: t, m: newModel(context.Background(), b, Options{SelfID: "alpha"})}
	h.m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	h.run(h.m.Init())
	h.press("x")
	if h.m.confirm != nil {
		t.Fatal("confirmation asked for removing the manager itself")
	}
	h.press("space")
	if len(b.Calls()) != 0 {
		t.Fatalf("calls = %q", b.Calls())
	}
	if !strings.Contains(h.screen(), "cannot remove or disable itself") {
		t.Errorf("no explanation shown:\n%s", h.screen())
	}
}

func TestSearchShowsWhyEntriesMatch(t *testing.T) {
	b := newFake()
	b.index = &market.Index{Repositories: []market.Repository{
		{Owner: "o", Name: "panes", Stars: 90, Topics: []string{"herdr-plugin", "ssh"},
			Manifests: []market.Manifest{{Path: "herdr-plugin.toml", ID: "o.panes", Name: "Remote Panes", Description: "Panes elsewhere"}}},
		{Owner: "o", Name: "ssh-manager", Stars: 1,
			Manifests: []market.Manifest{{Path: "herdr-plugin.toml", ID: "o.ssh-manager", Name: "SSH Manager"}}},
	}}
	h := start(t, b)
	h.press("tab", "/", "s", "s", "h", "tab")
	out := h.screen()
	name, topic := strings.Index(out, "SSH Manager"), strings.Index(out, "Remote Panes")
	if name < 0 || topic < 0 || name > topic {
		t.Errorf("a name match should rank above a topic match:\n%s", out)
	}
	if !strings.Contains(out, "topics: ssh") {
		t.Errorf("the matching topic is not shown:\n%s", out)
	}
	if strings.Contains(out, "herdr-plugin") {
		t.Errorf("the herdr-plugin topic should be left out:\n%s", out)
	}
	raw := h.m.View().Content
	if !strings.Contains(raw, "\x1b[4") && !strings.Contains(raw, ";4m") && !strings.Contains(raw, ";4;") {
		t.Errorf("no underlined match in the rendered view")
	}
}

func TestShutdownStopsRunningOperation(t *testing.T) {
	b := newFake()
	started, stopped := make(chan struct{}), make(chan struct{})
	b.installHook = func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		time.Sleep(20 * time.Millisecond)
		close(stopped)
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := newModel(ctx, b, Options{})
	cmd := m.install(installTarget{src: source.GitHub{Owner: "carol", Repo: "gadget"}}, "carol.gadget", nil)
	go cmd()
	<-started
	m.shutdown(cancel)
	select {
	case <-stopped:
	default:
		t.Fatal("shutdown returned while the install was still running")
	}
}

// Run with -race: enabling a plugin while a preview still reads the plugin
// list must not write to the elements the preview holds.
func TestEnablingDuringPreviewDoesNotRace(t *testing.T) {
	b := newFake()
	b.previewGate = make(chan struct{})
	h := start(t, b)
	d := &detail{loading: true, install: &installTarget{src: source.GitHub{Owner: "carol", Repo: "gadget"}}}
	cmd := h.m.loadPreview(d, source.GitHub{Owner: "carol", Repo: "gadget"}, "", "")
	done := make(chan struct{})
	go func() {
		cmd()
		close(done)
	}()
	h.m.Update(enabledMsg{id: "beta", enabled: true})
	close(b.previewGate)
	<-done
}

func TestStaleCheckIsIgnored(t *testing.T) {
	h := start(t, newFake())
	old := h.m.checkGen
	h.m.checkUpdates()
	h.m.Update(checksMsg{gen: old, results: []manager.Checked{{Plugin: plugin("alpha", true), Result: updates.Result{Kind: updates.UpToDate}}}})
	if !h.m.checking {
		t.Error("a stale result ended the check still running")
	}
	if ch := h.m.checks["alpha"]; ch.Result.Kind != updates.Available {
		t.Errorf("a stale result replaced the newer one: %+v", ch.Result)
	}
}

func TestMultiLineErrorKeepsTheScreenHeight(t *testing.T) {
	h := start(t, newFake())
	h.m.setStatus("install failed\nline 2\nline 3\nline 4\nline 5", true)
	if got := strings.Count(h.m.View().Content, "\n") + 1; got != 30 {
		t.Errorf("view is %d lines, want the terminal height 30", got)
	}
	if !strings.Contains(h.screen(), "install failed …") {
		t.Errorf("the status line should show the first line and mark the rest:\n%s", h.screen())
	}
}

func TestDetailOpensOnInfoAndTabSwitches(t *testing.T) {
	h := start(t, newFake())
	h.press("enter")
	out := h.screen()
	if !strings.Contains(out, "RECENT COMMAND LOGS") || !strings.Contains(out, "README") {
		t.Fatalf("the information should open first, with a README tab:\n%s", out)
	}
	h.press("tab")
	out = h.screen()
	if !strings.Contains(out, "The installed README.") || !strings.Contains(out, "/plugins/alpha/README.md") {
		t.Fatalf("tab should show the installed README:\n%s", out)
	}
	h.press("tab")
	if !strings.Contains(h.screen(), "RECENT COMMAND LOGS") {
		t.Fatalf("tab again should return to the information:\n%s", h.screen())
	}
}

func TestDetailWithoutReadmeHasNoReadmeTab(t *testing.T) {
	h := start(t, newFake())
	h.press("down", "enter")
	if strings.Contains(h.screen(), "README") {
		t.Fatalf("a plugin without a README should offer no README tab:\n%s", h.screen())
	}
	h.press("tab")
	if h.m.detail.view != viewInfo || !strings.Contains(h.screen(), "This plugin has no README") {
		t.Fatalf("tab should stay on the information and say why:\n%s", h.screen())
	}
}

func TestHomepageKey(t *testing.T) {
	b := newFake()
	h := start(t, b)
	h.press("w")
	h.press("tab", "enter", "w")
	want := []string{"open https://github.com/o/alpha", "open https://github.com/carol/gadget"}
	var got []string
	for _, c := range b.Calls() {
		if strings.HasPrefix(c, "open ") {
			got = append(got, c)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("opened %q, want %q", got, want)
	}
}

func TestMovingWhileTyping(t *testing.T) {
	b := newFake()
	b.index = &market.Index{Repositories: []market.Repository{
		{Owner: "o", Name: "a", Stars: 2, Manifests: []market.Manifest{{Path: "herdr-plugin.toml", ID: "o.ssh-a", Name: "SSH A"}}},
		{Owner: "o", Name: "b", Stars: 1, Manifests: []market.Manifest{{Path: "herdr-plugin.toml", ID: "o.ssh-b", Name: "SSH B"}}},
	}}
	h := start(t, b)
	h.press("tab", "/", "s", "s", "h", "ctrl+n")
	if !h.m.filters[tabBrowse].Focused() || h.m.cursor[tabBrowse] != 1 {
		t.Fatalf("ctrl+n should move while the search stays open: focused=%v cursor=%d",
			h.m.filters[tabBrowse].Focused(), h.m.cursor[tabBrowse])
	}
	h.press("ctrl+p", "down", "enter")
	if h.m.screen != screenDetail || h.m.detail.title != "SSH B" {
		t.Fatalf("enter should open the selected result, got screen %v", h.m.screen)
	}
}

func (h *harness) mouse(msg tea.Msg) {
	h.t.Helper()
	_, cmd := h.m.Update(msg)
	h.run(cmd)
}

// at finds text on the screen and returns its column and row.
func (h *harness) at(text string) (x, y int) {
	h.t.Helper()
	for y, line := range strings.Split(h.screen(), "\n") {
		if before, _, found := strings.Cut(line, text); found {
			return utf8.RuneCountInString(before), y
		}
	}
	h.t.Fatalf("%q is not on the screen:\n%s", text, h.screen())
	return 0, 0
}

func TestMouse(t *testing.T) {
	b := newFake()
	h := start(t, b)
	left := func(x, y int) tea.Msg { return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft} }

	x, y := h.at("beta")
	h.mouse(left(x, y))
	if h.m.cursor[tabInstalled] != 1 || h.m.screen != screenList {
		t.Fatalf("a click on beta should select it: cursor %d, screen %v", h.m.cursor[tabInstalled], h.m.screen)
	}
	h.mouse(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if h.m.cursor[tabInstalled] != 0 {
		t.Errorf("wheel up: cursor %d, want 0", h.m.cursor[tabInstalled])
	}
	x, y = h.at("alpha")
	h.mouse(left(x, y))
	if h.m.screen != screenDetail || h.m.detail.plugin == nil || h.m.detail.plugin.PluginID != "alpha" {
		t.Fatalf("a click on the selected item should open it:\n%s", h.screen())
	}
	x, y = h.at("README")
	h.mouse(left(x, y))
	if h.m.detail.view != viewReadme {
		t.Errorf("a click on the README tab should switch to it:\n%s", h.screen())
	}

	h.press("esc")
	x, y = h.at("Marketplace")
	h.mouse(left(x, y))
	if h.m.tab != tabBrowse {
		t.Errorf("a click on the Marketplace tab should switch to it:\n%s", h.screen())
	}
}

func TestMouseChoosesAVersion(t *testing.T) {
	b := newFake()
	b.preview.Ref, b.preview.DefaultBranch = "v1.1.0", "main"
	b.preview.Releases = []string{"v1.1.0", "v1.0.0"}
	h := start(t, b)
	h.press("tab", "enter", "v")
	x, y := h.at("v1.0.0")
	click := tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
	h.mouse(click)
	if h.m.detail.versions == nil || h.m.detail.versions.cursor != 1 {
		t.Fatalf("a click should select v1.0.0:\n%s", h.screen())
	}
	h.mouse(click)
	if got := b.Calls(); got[len(got)-1] != `preview carol/gadget "v1.0.0"` {
		t.Errorf("a second click should choose it: calls %q", got)
	}
}

func TestVersionPickerShowsReleaseNotes(t *testing.T) {
	b := newFake()
	b.preview.Ref, b.preview.DefaultBranch = "v1.1.0", "main"
	b.preview.Releases = []string{"v1.1.0", "v1.0.0"}
	b.releases = []market.Release{{Tag: "v1.1.0", Name: "Faster gadgets", Notes: "Gadgets start **twice** as fast."}}
	h := start(t, b)
	h.press("tab", "enter", "v")
	out := h.screen()
	if !strings.Contains(out, "Gadget › Versions") || strings.Contains(out, "README") {
		t.Errorf("the picker should have its own breadcrumb and no Info and README tabs:\n%s", out)
	}
	for _, want := range []string{"Faster gadgets", "Gadgets start twice as fast."} {
		if !strings.Contains(out, want) {
			t.Errorf("the notes of v1.1.0 lack %q:\n%s", want, out)
		}
	}
	h.press("down")
	if !strings.Contains(h.screen(), "This tag has no GitHub release") {
		t.Errorf("v1.0.0 has no release:\n%s", h.screen())
	}
	h.press("esc")
	if out := h.words(); !strings.Contains(out, "Info README") {
		t.Errorf("back in the details, the Info and README tabs are gone:\n%s", out)
	}
	h.press("v")
	n := 0
	for _, c := range b.Calls() {
		if strings.HasPrefix(c, "releases") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("releases read %d times, want once for the session", n)
	}
}

func TestVersionPickerExplainsTheRateLimit(t *testing.T) {
	b := &rateLimited{newFake()}
	b.preview.Ref, b.preview.Releases = "v1.1.0", []string{"v1.1.0"}
	h := start(t, b)
	h.press("tab", "enter", "v")
	if !strings.Contains(h.screen(), "set GH_TOKEN") {
		t.Errorf("the notes column should say how to raise the limit:\n%s", h.screen())
	}
}

type rateLimited struct{ *fakeBackend }

func (*rateLimited) Releases(context.Context, source.GitHub) ([]market.Release, error) {
	return nil, fmt.Errorf("read the releases: %w", market.ErrRateLimited)
}

func TestNoListKeyInstalls(t *testing.T) {
	b := newFake()
	h := start(t, b)
	h.press("tab", "i", "i")
	if got := b.Calls(); len(got) != 0 || h.m.screen != screenList {
		t.Fatalf("i in the marketplace list: screen %v, calls %q; want nothing", h.m.screen, got)
	}
	if words := h.words(); !strings.Contains(words, "enter preview") {
		t.Errorf("the list's main key should be enter preview:\n%s", h.screen())
	}
	h.press("enter", "i")
	if got := b.Calls(); len(got) != 2 || !strings.HasPrefix(got[1], "install") {
		t.Errorf("i in the preview should install: %q", got)
	}
}

func TestEnterInAPreviewDoesNotInstall(t *testing.T) {
	b := newFake()
	h := start(t, b)
	h.press("tab", "enter", "enter")
	for _, c := range b.Calls() {
		if strings.HasPrefix(c, "install") {
			t.Fatalf("a second enter installed: %q", b.Calls())
		}
	}
	if !strings.Contains(h.screen(), "Press i to install") {
		t.Errorf("no hint for the install key:\n%s", h.screen())
	}
}

func TestRollbackOfAnEnabledStateChangeAsksWithoutAPreview(t *testing.T) {
	before := manager.State{Version: "1.0.0", Source: "o/alpha", Ref: "v1.0.0", Commit: "c1", Enabled: true}
	after := before
	after.Enabled = false
	b := newFake()
	b.undo = manager.Undo{Entry: manager.Entry{Kind: manager.KindRestore, Plugin: "alpha", Before: &before, After: &after}, EnabledOnly: true}
	h := start(t, b)
	h.press("z")
	if h.m.confirm == nil || !strings.Contains(h.m.confirm.prompt, "Roll back alpha? That would undo the restore of alpha") ||
		!strings.Contains(h.m.confirm.prompt, "enable it again") {
		t.Fatalf("confirm = %+v", h.m.confirm)
	}
	h.press("y")
	if got := b.Calls(); !slices.Equal(got, []string{"plan-rollback alpha", "rollback alpha "}) {
		t.Errorf("calls = %q", got)
	}
}
