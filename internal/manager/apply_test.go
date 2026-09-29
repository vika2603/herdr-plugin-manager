package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vika2603/herdr-client/herdr"
	"github.com/vika2603/herdr-client/herdrtest"

	"github.com/vika2603/herdr-plugin-manager/internal/source"
	"github.com/vika2603/herdr-plugin-manager/internal/updates"
)

var (
	commitV1 = strings.Repeat("1", 40)
	commitV2 = strings.Repeat("2", 40)
)

// registry stands in for herdr's plugin registry. The fake herdr command
// installs by replacing the registry with the next one set, and a test
// server, when there is one, answers the plugin list and enables and
// disables plugins in it.
type registry struct {
	t      *testing.T
	file   string
	next   string
	m      *Manager
	calls  func() []string
	server *herdrtest.Server
}

// newRegistry starts with plugins. install is the shell run for
// `herdr plugin install`; by default it installs the next registry.
func newRegistry(t *testing.T, withServer bool, install string, plugins ...herdr.InstalledPluginInfo) *registry {
	t.Helper()
	dir := t.TempDir()
	r := &registry{t: t, file: filepath.Join(dir, "list.json"), next: filepath.Join(dir, "next.json")}
	r.write(r.file, plugins)
	if install == "" {
		install = "cp " + r.next + " " + r.file
	}
	cli, calls := fakeHerdr(t, `case "$*" in
"plugin list --json") cat `+r.file+` ;;
"plugin install "*) `+install+` ;;
esac`)
	r.calls = calls
	r.m = &Manager{CLI: cli, History: &History{Dir: filepath.Join(dir, "history")}}
	if withServer {
		r.server = herdrtest.NewServer(t).
			Reply(herdr.MethodPing, herdr.PongResponse{Version: "0.9.1"}).
			Handle(herdr.MethodPluginList, func(context.Context, herdrtest.Call) (herdr.Result, error) {
				return &herdr.PluginListResponse{Plugins: r.plugins()}, nil
			}).
			Handle(herdr.MethodPluginDisable, r.setEnabled(false)).
			Handle(herdr.MethodPluginEnable, r.setEnabled(true))
		r.m.API = r.server.Client()
	} else {
		r.m.API = herdr.New(filepath.Join(dir, "missing.sock"))
	}
	return r
}

func (r *registry) setEnabled(enabled bool) herdrtest.Handler {
	return func(_ context.Context, c herdrtest.Call) (herdr.Result, error) {
		var params herdr.PluginSetEnabledParams
		if err := json.Unmarshal(c.Params, &params); err != nil {
			return nil, err
		}
		plugins := r.plugins()
		for i := range plugins {
			if plugins[i].PluginID == params.PluginID {
				plugins[i].Enabled = enabled
			}
		}
		r.write(r.file, plugins)
		if enabled {
			return &herdr.PluginEnabledResponse{}, nil
		}
		return &herdr.PluginDisabledResponse{}, nil
	}
}

func (r *registry) write(path string, plugins []herdr.InstalledPluginInfo) {
	r.t.Helper()
	if plugins == nil {
		plugins = []herdr.InstalledPluginInfo{}
	}
	data, err := json.Marshal(map[string]any{"result": herdr.PluginListResponse{Plugins: plugins}})
	if err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		r.t.Fatal(err)
	}
}

func (r *registry) plugins() []herdr.InstalledPluginInfo {
	r.t.Helper()
	data, err := os.ReadFile(r.file)
	if err != nil {
		r.t.Fatal(err)
	}
	var envelope struct {
		Result herdr.PluginListResponse `json:"result"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		r.t.Fatal(err)
	}
	return envelope.Result.Plugins
}

// installs sets what the next install leaves in the registry.
func (r *registry) installs(plugins ...herdr.InstalledPluginInfo) { r.write(r.next, plugins) }

func (r *registry) installed() bool {
	return slices.ContainsFunc(r.calls(), func(c string) bool { return strings.HasPrefix(c, "plugin install") })
}

// at is plugin o.r from o/r, installed from ref at commit.
func at(ref, commit string, enabled bool) herdr.InstalledPluginInfo {
	return herdr.InstalledPluginInfo{
		PluginID: "o.r", Name: "R", Version: map[string]string{commitV1: "1.0.0", commitV2: "2.0.0"}[commit], Enabled: enabled,
		ManifestPath: "/x", PluginRoot: "/x",
		Source: herdr.Some(herdr.PluginSourceInfo{
			Kind:  herdr.Some(herdr.PluginSourceKindGithub),
			Owner: herdr.Some("o"), Repo: herdr.Some("r"),
			RequestedRef: herdr.Some(ref), ResolvedCommit: herdr.Some(commit),
		}),
	}
}

// releases is a remote with v1.0.0 and v2.0.0, and main at v2.0.0.
var releases = fakeLister{updates.Refs{
	Head: commitV2, HeadBranch: "main", Branches: map[string]string{"main": commitV2},
	Tags: map[string]string{"v1.0.0": commitV1, "v2.0.0": commitV2},
}}

var src = source.GitHub{Owner: "o", Repo: "r"}

func update(r *registry) Outcome {
	current := at("v1.0.0", commitV1, false)
	return r.m.Apply(context.Background(), Change{
		Kind: KindUpdate, ID: "o.r", Current: &current,
		Target: Target{Source: src, Ref: "v2.0.0", Commit: commitV2},
	}, nil)
}

func TestApplyKeepsADisabledPluginDisabled(t *testing.T) {
	r := newRegistry(t, true, "", at("v1.0.0", commitV1, false))
	r.m.Git = releases
	r.installs(at("v2.0.0", commitV2, true))

	o := update(r)
	if o.Err != nil {
		t.Fatal(o.Error())
	}
	if got := r.plugins()[0]; got.Enabled {
		t.Error("the plugin was left enabled")
	}
	if o.After == nil || o.After.Enabled || o.After.Commit != commitV2 {
		t.Errorf("after = %+v", o.After)
	}
	if !strings.Contains(o.Summary(), "1.0.0 at v1.0.0 (111111111111), disabled -> 2.0.0 at v2.0.0 (222222222222), disabled") {
		t.Errorf("summary = %q", o.Summary())
	}
	entries, _ := r.m.HistoryEntries()
	if len(entries) != 1 || entries[0].Kind != KindUpdate || entries[0].Failed() || entries[0].Before.Commit != commitV1 || entries[0].After.Commit != commitV2 {
		t.Fatalf("history = %+v", entries)
	}
	if log, err := ReadLog(entries[0]); err != nil || !strings.Contains(log, "update o.r at o/r @ v2.0.0") {
		t.Errorf("log = %q, %v", log, err)
	}
}

func TestApplyWillNotLeaveADisabledPluginEnabledWithoutServer(t *testing.T) {
	r := newRegistry(t, false, "", at("v1.0.0", commitV1, false))
	r.m.Git = releases
	r.installs(at("v2.0.0", commitV2, true))

	o := update(r)
	if !errors.Is(o.Err, ErrKeepDisabled) {
		t.Fatalf("err = %v, want ErrKeepDisabled", o.Err)
	}
	if r.installed() {
		t.Errorf("herdr installed although the plugin could not be disabled again: %q", r.calls())
	}
	if !strings.Contains(o.Error().Error(), "o.r is unchanged: 1.0.0 at v1.0.0 (111111111111), disabled") {
		t.Errorf("the error does not say where the plugin stands: %v", o.Error())
	}
}

func TestApplyReportsWhereAFailedInstallLeftThePlugin(t *testing.T) {
	// herdr leaves the installed plugin in place when a build fails.
	r := newRegistry(t, true, "echo 'build failed'; exit 1", at("v1.0.0", commitV1, true))
	r.m.Git = releases

	o := update(r)
	if o.Err == nil {
		t.Fatal("a failed install succeeded")
	}
	msg := o.Error().Error()
	if !strings.Contains(msg, "build failed") || !strings.Contains(msg, "o.r is unchanged: 1.0.0 at v1.0.0 (111111111111), enabled") {
		t.Errorf("error = %q", msg)
	}
	entries, _ := r.m.HistoryEntries()
	if len(entries) != 1 || !entries[0].Failed() || !entries[0].After.SameRevision(*entries[0].Before) {
		t.Errorf("history = %+v", entries)
	}
}

func TestApplyInstallsTheCommitThePreviewShowed(t *testing.T) {
	// The default branch has moved on from the commit previewed; herdr is
	// asked for that commit, and records it as a pin.
	r := newRegistry(t, true, "", at("", commitV1, true))
	r.m.Git = headAt(strings.Repeat("3", 40))
	r.installs(at(commitV2, commitV2, true))
	current := at("", commitV1, true)
	o := r.m.Apply(context.Background(), Change{Kind: KindUpdate, ID: "o.r", Current: &current, Target: Target{Source: src, Commit: commitV2}}, nil)
	if o.Err != nil {
		t.Fatal(o.Error())
	}
	if !slices.Contains(r.calls(), "plugin install o/r --ref "+commitV2+" --yes") {
		t.Fatalf("herdr was not asked for the previewed commit: %q", r.calls())
	}
	// The plugin still follows the default branch.
	if o.After == nil || o.After.Ref != "" || o.After.Commit != commitV2 {
		t.Errorf("after = %+v, want the default branch at the previewed commit", o.After)
	}
	plugins, _ := r.m.Installed(context.Background())
	if tr := TrackingOf(plugins[0]); tr.Kind != TrackDefault || tr.Commit != commitV2 {
		t.Errorf("tracking = %+v", tr)
	}
	res, err := r.m.Check(context.Background(), plugins[0])
	if err != nil || res.Kind != updates.Available {
		t.Errorf("the moved branch is not an update: %+v, %v", res, err)
	}
}

func TestAFollowOnlyHoldsWhileHerdrKeepsThePin(t *testing.T) {
	r := newRegistry(t, true, "", at("v1.0.0", commitV1, true))
	r.m.Git = releases
	r.installs(at(commitV2, commitV2, true))
	if o := update(r); o.Err != nil {
		t.Fatal(o.Error())
	}
	plugins, _ := r.m.Installed(context.Background())
	if got := TrackingOf(plugins[0]).Describe(); got != "follows new releases, installed at v2.0.0" {
		t.Errorf("tracking = %q", got)
	}
	// Installed outside this manager at another ref: herdr's record wins.
	r.write(r.file, []herdr.InstalledPluginInfo{at("main", commitV2, true)})
	plugins, _ = r.m.Installed(context.Background())
	if got := TrackingOf(plugins[0]).Describe(); got != "follows main when it moves" {
		t.Errorf("tracking = %q", got)
	}
	// A pin asked for as such follows nothing.
	r.write(r.file, []herdr.InstalledPluginInfo{at("v2.0.0", commitV2, true)})
	r.installs(at(commitV2, commitV2, true))
	current := at("v2.0.0", commitV2, true)
	if o := r.m.Apply(context.Background(), Change{Kind: KindPin, ID: "o.r", Current: &current, Target: Target{Source: src, Ref: commitV2, Commit: commitV2}}, nil); o.Err != nil {
		t.Fatal(o.Error())
	}
	plugins, _ = r.m.Installed(context.Background())
	if got := TrackingOf(plugins[0]).Kind; got != TrackPinned {
		t.Errorf("after a pin: %v", got)
	}
}

func TestApplyReportsAnotherInstalledCommit(t *testing.T) {
	r := newRegistry(t, true, "", at("v1.0.0", commitV1, true))
	r.m.Git = releases
	r.installs(at("v2.0.0", commitV1, true))
	o := r.m.Apply(context.Background(), Change{Kind: KindUpdate, ID: "o.r", Target: Target{Source: src, Ref: "v2.0.0", Commit: commitV2}}, nil)
	if o.Err == nil || !strings.Contains(o.Err.Error(), "not the previewed") {
		t.Fatalf("err = %v, want a report that another commit was installed", o.Err)
	}
}

func TestApplyDoesNotConfirmWhatItCannotReadBack(t *testing.T) {
	r := newRegistry(t, false, "", at("v1.0.0", commitV1, true))
	// Once installed, the plugin list is unreadable.
	cli, _ := fakeHerdr(t, `case "$*" in
"plugin list --json") if [ -f `+r.next+` ]; then printf '{'; else cat `+r.file+`; fi ;;
"plugin install "*) touch `+r.next+` ;;
esac`)
	r.m.CLI, r.m.Git = cli, releases
	o := r.m.Apply(context.Background(), Change{Kind: KindUpdate, ID: "o.r", Target: Target{Source: src, Ref: "v2.0.0", Commit: commitV2}}, nil)
	if !o.AfterUnknown || !errors.Is(o.Error(), ErrUnconfirmed) {
		t.Fatalf("after unknown %v, err %v; want an unconfirmed change", o.AfterUnknown, o.Error())
	}
	if !strings.Contains(o.Error().Error(), "check it with hpm info o.r") {
		t.Errorf("the error does not say how to check: %v", o.Error())
	}
	entries, _ := r.m.HistoryEntries()
	if len(entries) != 1 || !entries[0].Failed() || !entries[0].AfterUnknown {
		t.Errorf("history = %+v, want the change recorded as not confirmed", entries)
	}
}

func TestApplyReportsAPluginLeftEnabled(t *testing.T) {
	r := newRegistry(t, true, "", at("v1.0.0", commitV1, false))
	r.server.Fail(herdr.MethodPluginDisable, "internal", "disk full")
	r.m.Git = releases
	r.installs(at("v2.0.0", commitV2, true))

	o := update(r)
	if o.Err == nil || !strings.Contains(o.Error().Error(), "herdr left o.r enabled, and it could not be disabled again") {
		t.Fatalf("err = %v", o.Error())
	}
	if o.After == nil || !o.After.Enabled || o.After.Commit != commitV2 {
		t.Errorf("after = %+v, want the new version, enabled", o.After)
	}
}

func TestRollback(t *testing.T) {
	t.Run("an update returns to the release it replaced", func(t *testing.T) {
		r := newRegistry(t, true, "", at("v1.0.0", commitV1, false))
		r.m.Git = releases
		r.installs(at("v2.0.0", commitV2, true))
		if o := update(r); o.Err != nil {
			t.Fatal(o.Error())
		}
		u, err := r.m.PlanRollback(context.Background(), "o.r")
		if err != nil {
			t.Fatal(err)
		}
		if u.Remove || u.Target.Ref != "v1.0.0" || u.Target.Commit != commitV1 || *u.Target.Enabled {
			t.Fatalf("undo = %+v", u)
		}
		r.installs(at("v1.0.0", commitV1, true))
		o := r.m.Rollback(context.Background(), u, nil)
		if o.Err != nil {
			t.Fatal(o.Error())
		}
		if got := r.plugins()[0]; got.Enabled || StateOf(got).Commit != commitV1 {
			t.Errorf("after the rollback: %+v", StateOf(got))
		}
	})
	t.Run("a branch that moved on still follows it", func(t *testing.T) {
		r := newRegistry(t, true, "", at("", commitV1, true))
		r.m.Git = releases
		r.installs(at("", commitV2, true))
		current := at("", commitV1, true)
		if o := r.m.Apply(context.Background(), Change{Kind: KindUpdate, ID: "o.r", Current: &current, Target: Target{Source: src, Commit: commitV2}}, nil); o.Err != nil {
			t.Fatal(o.Error())
		}
		u, err := r.m.PlanRollback(context.Background(), "o.r")
		if err != nil {
			t.Fatal(err)
		}
		if u.Target.Ref != "" || u.Target.Commit != commitV1 {
			t.Errorf("undo = %+v: %s", u, u.Describe())
		}
	})
	t.Run("an install is undone by uninstalling", func(t *testing.T) {
		r := newRegistry(t, true, "")
		r.m.Git = releases
		r.installs(at("v2.0.0", commitV2, true))
		if o := r.m.Apply(context.Background(), Change{Kind: KindInstall, ID: "o.r", Target: Target{Source: src, Ref: "v2.0.0", Commit: commitV2}}, nil); o.Err != nil {
			t.Fatal(o.Error())
		}
		u, err := r.m.PlanRollback(context.Background(), "o.r")
		if err != nil || !u.Remove {
			t.Fatalf("undo = %+v, %v", u, err)
		}
	})
	t.Run("a change made elsewhere is not undone", func(t *testing.T) {
		r := newRegistry(t, true, "", at("v1.0.0", commitV1, true))
		r.m.Git = releases
		r.installs(at("v2.0.0", commitV2, true))
		if o := update(r); o.Err != nil {
			t.Fatal(o.Error())
		}
		r.write(r.file, []herdr.InstalledPluginInfo{at("main", commitV2, true)})
		if _, err := r.m.PlanRollback(context.Background(), "o.r"); !errors.Is(err, ErrNothingToUndo) {
			t.Errorf("err = %v, want ErrNothingToUndo", err)
		}
	})
	t.Run("a failed change that changed nothing is passed over", func(t *testing.T) {
		r := newRegistry(t, true, "", at("v1.0.0", commitV1, true))
		r.m.Git = releases
		r.installs(at("v2.0.0", commitV2, true))
		if o := update(r); o.Err != nil {
			t.Fatal(o.Error())
		}
		r.m.CLI = newRegistry(t, true, "exit 1").m.CLI
		current := at("v2.0.0", commitV2, true)
		if o := r.m.Apply(context.Background(), Change{Kind: KindSwitch, ID: "o.r", Current: &current, Target: Target{Source: src, Ref: "v1.0.0", Commit: commitV1}}, nil); o.Err == nil {
			t.Fatal("the failing install succeeded")
		}
		u, err := r.m.PlanRollback(context.Background(), "o.r")
		if err != nil || u.Entry.Kind != KindUpdate || u.Target.Ref != "v1.0.0" {
			t.Errorf("undo = %+v, %v; want the update undone", u, err)
		}
	})
	t.Run("nothing recorded", func(t *testing.T) {
		r := newRegistry(t, true, "", at("v1.0.0", commitV1, true))
		if _, err := r.m.PlanRollback(context.Background(), "o.r"); !errors.Is(err, ErrNothingToUndo) {
			t.Errorf("err = %v, want ErrNothingToUndo", err)
		}
	})
}

func TestHistoryIsTrimmed(t *testing.T) {
	h := &History{Dir: t.TempDir()}
	for i := range historyMax + 1 {
		if err := h.add(&Entry{Kind: KindUpdate, Plugin: "p" + string(rune('a'+i%26))}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := h.List()
	if err != nil || len(list) != historyKeep {
		t.Fatalf("kept %d entries, %v; want %d", len(list), err, historyKeep)
	}
}

func TestApplyRestoresTheEnabledStateAfterAFailureThatRegistered(t *testing.T) {
	// herdr registered the new version, enabled, and then failed.
	r := newRegistry(t, true, "", at("v1.0.0", commitV1, false))
	r.m.Git = releases
	r.installs(at("v2.0.0", commitV2, true))
	cli, _ := fakeHerdr(t, `case "$*" in
"plugin install "*) cp `+r.next+` `+r.file+`; exit 7 ;;
esac`)
	r.m.CLI = cli
	o := update(r)
	if o.Err == nil {
		t.Fatal("a failed install succeeded")
	}
	if r.plugins()[0].Enabled || o.After == nil || o.After.Enabled || o.After.Commit != commitV2 {
		t.Errorf("registry enabled %v, after %+v; want it disabled again and reported so", r.plugins()[0].Enabled, o.After)
	}
	if !strings.Contains(o.Error().Error(), "o.r: 1.0.0 at v1.0.0 (111111111111), disabled -> 2.0.0 at v2.0.0 (222222222222), disabled") {
		t.Errorf("the error does not say where the plugin stands: %v", o.Error())
	}
	entries, _ := r.m.HistoryEntries()
	if len(entries) != 1 || entries[0].After == nil || entries[0].After.Enabled {
		t.Errorf("history = %+v", entries)
	}

	t.Run("and says when it cannot", func(t *testing.T) {
		r := newRegistry(t, true, "", at("v1.0.0", commitV1, false))
		r.server.Fail(herdr.MethodPluginDisable, "internal", "disk full")
		r.m.Git = releases
		r.installs(at("v2.0.0", commitV2, true))
		cli, _ := fakeHerdr(t, `case "$*" in
"plugin install "*) cp `+r.next+` `+r.file+`; exit 7 ;;
esac`)
		r.m.CLI = cli
		o := update(r)
		if o.Err == nil || !strings.Contains(o.Error().Error(), "herdr left o.r enabled, and it could not be disabled again") {
			t.Errorf("err = %v", o.Error())
		}
	})
}

func TestUninstallRecordsWhatIsLeft(t *testing.T) {
	t.Run("unregistered, then the files failed", func(t *testing.T) {
		r := newRegistry(t, true, "", at("v1.0.0", commitV1, false))
		r.m.Git = releases
		r.installs()
		cli, _ := fakeHerdr(t, `case "$*" in
"plugin uninstall "*) cp `+r.next+` `+r.file+`; exit 7 ;;
esac`)
		r.m.CLI = cli
		if err := r.m.Uninstall(context.Background(), "o.r", nil); err == nil || !strings.Contains(err.Error(), "o.r is no longer installed") {
			t.Fatalf("err = %v, want the failure and that o.r is gone", err)
		}
		entries, _ := r.m.HistoryEntries()
		if len(entries) != 1 || !entries[0].Failed() || entries[0].After != nil || entries[0].AfterUnknown {
			t.Fatalf("history = %+v, want o.r recorded as removed", entries)
		}
		u, err := r.m.PlanRollback(context.Background(), "o.r")
		if err != nil || u.Remove || u.Target.Commit != commitV1 || *u.Target.Enabled {
			t.Errorf("undo = %+v, %v; want o.r back at v1.0.0, disabled", u, err)
		}
	})
	t.Run("unreadable afterwards", func(t *testing.T) {
		r := newRegistry(t, false, "", at("v1.0.0", commitV1, true))
		cli, _ := fakeHerdr(t, `case "$*" in
"plugin list --json") if [ -f `+r.next+` ]; then printf '{'; else cat `+r.file+`; fi ;;
"plugin uninstall "*) touch `+r.next+` ;;
esac`)
		r.m.CLI = cli
		if err := r.m.Uninstall(context.Background(), "o.r", nil); err == nil || !strings.Contains(err.Error(), "not confirmed") {
			t.Fatalf("err = %v, want the removal unconfirmed", err)
		}
		entries, _ := r.m.HistoryEntries()
		if len(entries) != 1 || !entries[0].AfterUnknown {
			t.Errorf("history = %+v, want the state unknown", entries)
		}
	})
	t.Run("a rollback that uninstalls is recorded too", func(t *testing.T) {
		r := newRegistry(t, true, "")
		r.m.Git = releases
		r.installs(at("v2.0.0", commitV2, true))
		if o := r.m.Apply(context.Background(), Change{Kind: KindInstall, ID: "o.r", Target: Target{Source: src, Ref: "v2.0.0", Commit: commitV2}}, nil); o.Err != nil {
			t.Fatal(o.Error())
		}
		u, err := r.m.PlanRollback(context.Background(), "o.r")
		if err != nil || !u.Remove {
			t.Fatalf("undo = %+v, %v", u, err)
		}
		r.installs()
		cli, _ := fakeHerdr(t, `case "$*" in
"plugin uninstall "*) cp `+r.next+` `+r.file+` ;;
esac`)
		r.m.CLI = cli
		if o := r.m.Rollback(context.Background(), u, nil); o.Err != nil || o.After != nil || o.Before == nil {
			t.Errorf("outcome = %+v", o)
		}
		entries, _ := r.m.HistoryEntries()
		if last := entries[len(entries)-1]; last.Kind != KindRollback || last.After != nil || last.Before == nil {
			t.Errorf("last entry = %+v", last)
		}
	})
}

func TestCancelledInstallIsRecordedAsCancelled(t *testing.T) {
	r := newRegistry(t, true, "echo building; exec sleep 30", at("v1.0.0", commitV1, true))
	r.m.Git = releases
	ctx, cancel := context.WithCancel(context.Background())
	out := &syncBuffer{}
	go func() {
		for !strings.Contains(out.String(), "building") {
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	current := at("v1.0.0", commitV1, true)
	began := time.Now()
	o := r.m.Apply(ctx, Change{Kind: KindUpdate, ID: "o.r", Current: &current,
		Target: Target{Source: src, Ref: "v2.0.0", Commit: commitV2}}, out)
	if waited := time.Since(began); waited > 5*time.Second {
		t.Errorf("the interrupted build ran on for %v", waited)
	}
	if !errors.Is(o.Err, ErrCancelled) {
		t.Fatalf("err = %v, want it cancelled", o.Err)
	}
	if o.After == nil || o.After.Commit != commitV1 {
		t.Errorf("after = %+v, want the plugin left at %s", o.After, commitV1)
	}
	entries, err := r.m.HistoryEntries()
	if err != nil || len(entries) != 1 || entries[0].Result() != "cancelled" {
		t.Fatalf("history = %+v, %v; want one cancelled change", entries, err)
	}
}

// syncBuffer is a buffer written by one goroutine while another reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
