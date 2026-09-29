package manager

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-plugin-manager/internal/market"
)

// commitGone has no manifest upstream, as after a force push.
var commitGone = strings.Repeat("9", 40)

// ghPlugin is plugin id installed from repo, an owner/name pair, at ref and
// commit.
func ghPlugin(id, repo, ref, commit string, enabled bool) herdr.InstalledPluginInfo {
	owner, name, _ := strings.Cut(repo, "/")
	return herdr.InstalledPluginInfo{
		PluginID: id, Name: id, Version: map[string]string{commitV1: "1.0.0", commitV2: "2.0.0"}[commit], Enabled: enabled,
		ManifestPath: "/x", PluginRoot: "/x",
		Source: herdr.Some(herdr.PluginSourceInfo{
			Kind:  herdr.Some(herdr.PluginSourceKindGithub),
			Owner: herdr.Some(owner), Repo: herdr.Some(name),
			RequestedRef: herdr.Some(ref), ResolvedCommit: herdr.Some(commit),
		}),
	}
}

func linkedPlugin(id, root string) herdr.InstalledPluginInfo {
	return herdr.InstalledPluginInfo{
		PluginID: id, Name: id, Version: "0.1.0", Enabled: true, ManifestPath: root + "/herdr-plugin.toml", PluginRoot: root,
		Source: herdr.Some(herdr.PluginSourceInfo{Kind: herdr.Some(herdr.PluginSourceKindLocal)}),
	}
}

// serveManifests gives r's manager a GitHub whose manifest in each repo
// declares the id ids maps it to, at every commit but commitGone.
func serveManifests(t *testing.T, r *registry, ids map[string]string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		parts := strings.Split(strings.Trim(req.URL.Path, "/"), "/")
		id, ok := ids[parts[0]+"/"+parts[1]]
		if len(parts) != 4 || !ok || parts[2] == commitGone {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write([]byte("id = \"" + id + "\"\nname = \"N\"\nversion = \"2.0.0\"\nmin_herdr_version = \"0.9.0\"\n[[build]]\ncommand = [\"make\"]\n"))
	}))
	t.Cleanup(server.Close)
	mc := market.NewClient("", "test")
	mc.HTTP = &http.Client{Transport: redirect{server.URL}}
	r.m.Market = mc
}

func exported(id, repo, ref, commit string, enabled bool) ExportedPlugin {
	return ExportedPlugin{ID: id, Enabled: new(enabled), Kind: ExportGitHub, Source: repo, Ref: ref, Commit: commit,
		Tracking: TrackingAt(ref, commit).Kind}
}

func TestExportRecordsWhatEachPluginFollows(t *testing.T) {
	r := newRegistry(t, true, "", at("v1.0.0", commitV1, true))
	r.m.Git = releases
	// The update asks herdr for the commit, which herdr records as a pin;
	// the export has the release the plugin follows.
	r.installs(at(commitV2, commitV2, true))
	if o := update(r); o.Err != nil {
		t.Fatal(o.Error())
	}
	plugins := append(r.plugins(), linkedPlugin("me.dev", "/src/dev"), ghPlugin("o.off", "o/off", "main", commitV1, false))
	r.write(r.file, plugins)

	exp, err := r.m.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if exp.Format != ExportFormat || exp.Version != ExportVersion || exp.HerdrVersion != "0.9.1" {
		t.Errorf("header = %+v", exp)
	}
	byID := map[string]ExportedPlugin{}
	for _, p := range exp.Plugins {
		byID[p.ID] = p
	}
	if p := byID["o.r"]; p.Kind != ExportGitHub || p.Source != "o/r" || p.Ref != "v2.0.0" || p.Commit != commitV2 || p.Tracking != TrackRelease || !p.IsEnabled() {
		t.Errorf("o.r = %+v", p)
	}
	if p := byID["o.off"]; p.Ref != "main" || p.Tracking != TrackRef || p.IsEnabled() {
		t.Errorf("o.off = %+v", p)
	}
	if _, ok := byID["me.dev"]; ok || !slices.Equal(exp.Local, []string{"me.dev"}) {
		t.Errorf("the local link is not skipped: %+v, local %v", byID["me.dev"], exp.Local)
	}

	var buf bytes.Buffer
	if err := WriteExport(&buf, exp); err != nil {
		t.Fatal(err)
	}
	back, err := ReadExport(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Plugins) != 2 || back.Plugins[1].Ref != exp.Plugins[1].Ref || !back.ExportedAt.Equal(exp.ExportedAt) {
		t.Errorf("read back = %+v", back)
	}
}

func TestExportFailsWithoutTheKeptRefs(t *testing.T) {
	r := newRegistry(t, true, "", at(commitV2, commitV2, true))
	if err := os.MkdirAll(r.m.History.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.m.History.Dir, followsFile), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Exported as herdr records it, the plugin would be a commit pin.
	if _, err := r.m.Export(context.Background()); err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Errorf("err = %v", err)
	}
}

func TestReadExportRefusesWhatItWouldMisread(t *testing.T) {
	const plugin = `{"id":"o.r","enabled":true,"kind":"github","source":"o/r","commit":"` + "1111111111111111111111111111111111111111" + `"}`
	for _, tt := range []struct{ name, file, want string }{
		{"not JSON", `plugins`, "not an hpm plugin export"},
		{"another format", `{"format":"npm","version":1}`, `format is "npm"`},
		{"a newer version", `{"format":"hpm-plugins","version":2,"plugins":[]}`, "from a newer hpm"},
		{"an unknown field", `{"format":"hpm-plugins","version":1,"plugins":[{"id":"o.r","enable":false,"enabled":true,"kind":"github"}]}`, `unknown field "enable"`},
		{"a plugin twice", `{"format":"hpm-plugins","version":1,"plugins":[` + plugin + `,` + plugin + `]}`, "lists o.r more than once"},
		{"no enabled state", `{"format":"hpm-plugins","version":1,"plugins":[{"id":"o.r","kind":"github"}]}`, "whether it is enabled"},
		{"no id", `{"format":"hpm-plugins","version":1,"plugins":[{"enabled":true,"kind":"github"}]}`, "has no id"},
		{"no kind", `{"format":"hpm-plugins","version":1,"plugins":[{"id":"o.r","enabled":true}]}`, "where it was installed from"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ReadExport(strings.NewReader(tt.file)); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

// planFixture is a registry with a server, the plugins installed here, and
// an export that meets each of them in another way.
func planFixture(t *testing.T, withServer bool) (*registry, *Export) {
	t.Helper()
	r := newRegistry(t, withServer, "",
		ghPlugin("o.same", "o/same", "v1.0.0", commitV1, true),
		ghPlugin("o.toggle", "o/toggle", "v1.0.0", commitV1, true),
		ghPlugin("o.older", "o/older", "v1.0.0", commitV1, true),
		ghPlugin("o.moved", "x/moved", "", commitV1, true),
		linkedPlugin("o.linked", "/src/linked"),
		ghPlugin("z.extra", "z/extra", "", commitV1, true),
	)
	r.m.Git = releases
	serveManifests(t, r, map[string]string{
		"o/same": "o.same", "o/toggle": "o.toggle", "o/older": "o.older", "o/new": "o.new",
		"o/off": "o.off", "o/gone": "o.gone", "o/renamed": "someone.else", "o/linked": "o.linked",
	})
	noCommit := exported("o.nocommit", "o/nocommit", "main", "", true)
	badTracking := exported("o.badtrack", "o/badtrack", "main", commitV1, true)
	badTracking.Tracking = TrackPinned
	return r, &Export{Format: ExportFormat, Version: ExportVersion, Plugins: []ExportedPlugin{
		exported("o.same", "o/same", "v1.0.0", commitV1, true),
		exported("o.toggle", "o/toggle", "v1.0.0", commitV1, false),
		exported("o.older", "o/older", "v2.0.0", commitV2, true),
		exported("o.new", "o/new", "v1.0.0", commitV1, true),
		exported("o.off", "o/off", "", commitV1, false),
		exported("o.gone", "o/gone", "", commitGone, true),
		exported("o.renamed", "o/renamed", "", commitV2, true),
		exported("o.moved", "o/moved", "", commitV1, true),
		exported("o.linked", "o/linked", "", commitV1, true),
		{ID: "me.dev", Enabled: new(true), Kind: ExportLocal, Root: "/src/dev"},
		{ID: "o.archive", Enabled: new(true), Kind: "archive"},
		noCommit,
		badTracking,
	}}
}

func TestPlanRestore(t *testing.T) {
	r, exp := planFixture(t, true)
	plan, err := r.m.PlanRestore(context.Background(), exp, nil, "0.9.1")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(plan.Others, []string{"z.extra"}) || plan.Left != nil {
		t.Errorf("others = %v, left = %v", plan.Others, plan.Left)
	}
	byID := map[string]RestoreItem{}
	for _, it := range plan.Items {
		byID[it.Plugin.ID] = it
	}
	for _, tt := range []struct {
		id      string
		action  RestoreAction
		runs    bool
		blocker string
	}{
		{"o.same", RestoreUnchanged, false, ""},
		{"o.toggle", RestoreDisable, true, ""},
		{"o.older", RestoreChange, true, ""},
		{"o.new", RestoreInstall, true, ""},
		{"o.off", RestoreInstall, true, ""},
		{"o.gone", RestoreInstall, false, "the manifest at the exported commit could not be read"},
		{"o.renamed", RestoreInstall, false, "the plugin id changed upstream"},
		{"o.moved", RestoreConflict, false, "installed here from x/moved, not o/moved"},
		{"o.linked", RestoreConflict, false, "linked locally here from /src/linked"},
		{"me.dev", RestoreLocal, false, "linked locally from /src/dev where it was exported"},
		{"o.archive", RestoreUnsupported, false, `source as "archive"`},
		{"o.nocommit", RestoreUnsupported, false, "no full commit hash"},
		{"o.badtrack", RestoreUnsupported, false, `its tracking is "pinned"`},
	} {
		it, ok := byID[tt.id]
		switch {
		case !ok:
			t.Errorf("%s is not in the plan", tt.id)
		case it.Action != tt.action || it.Runs() != tt.runs || !strings.Contains(it.Blocker, tt.blocker) || tt.blocker == "" && it.Blocker != "":
			t.Errorf("%s: action %s, runs %v, blocker %q; want %s, %v, %q", tt.id, it.Action, it.Runs(), it.Blocker, tt.action, tt.runs, tt.blocker)
		}
	}
	if it := byID["o.older"]; it.Explain == nil || !strings.Contains(it.Explain.Headline, "New release v2.0.0, replacing v1.0.0") {
		t.Errorf("the change is not explained: %+v", it.Explain)
	}
	if it := byID["o.new"]; it.Preview == nil || it.Preview.Commit != commitV1 || it.Preview.Ref != "v1.0.0" ||
		it.Target.Commit != commitV1 || it.Target.Ref != "v1.0.0" || !*it.Target.Enabled {
		t.Errorf("o.new is not previewed and targeted at the exported commit: %+v", it)
	}
	// main has moved on to v2.0.0 since o.off was exported at v1.0.0.
	if it := byID["o.off"]; len(it.Notes) != 1 || !strings.Contains(it.Notes[0], "available as an update afterwards") {
		t.Errorf("o.off notes = %q", it.Notes)
	}
	if got := byID["o.older"].Describe(); got != "change 1.0.0 at v1.0.0 (111111111111), enabled -> 2.0.0 at v2.0.0 (222222222222), enabled" {
		t.Errorf("describe = %q", got)
	}
	if r.installed() {
		t.Errorf("planning installed: %q", r.calls())
	}
}

func TestPlanRestoreWithoutServer(t *testing.T) {
	r, exp := planFixture(t, false)
	plan, err := r.m.PlanRestore(context.Background(), exp, []string{"o.toggle", "o.off", "o.new"}, "0.9.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 3 || len(plan.Left) != len(exp.Plugins)-3 {
		t.Fatalf("items %d, left %v", len(plan.Items), plan.Left)
	}
	for _, it := range plan.Items {
		wantRuns := it.Plugin.ID == "o.new"
		if it.Runs() != wantRuns || !wantRuns && !strings.Contains(it.Blocker, "running herdr server") {
			t.Errorf("%s: runs %v, blocker %q", it.Plugin.ID, it.Runs(), it.Blocker)
		}
	}
	if _, err := r.m.PlanRestore(context.Background(), exp, []string{"o.nope"}, "0.9.1"); err == nil {
		t.Error("an id the export does not list was planned")
	}
}

func TestRestoreInstallsTheExportedCommitAndKeepsItsState(t *testing.T) {
	r := newRegistry(t, true, "")
	r.m.Git = releases
	serveManifests(t, r, map[string]string{"o/r": "o.r"})
	exp := &Export{Format: ExportFormat, Version: ExportVersion, Plugins: []ExportedPlugin{exported("o.r", "o/r", "v1.0.0", commitV1, false)}}
	plan, err := r.m.PlanRestore(context.Background(), exp, nil, "0.9.1")
	if err != nil || len(plan.Items) != 1 || !plan.Items[0].Runs() {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	r.installs(at(commitV1, commitV1, true))
	o := r.m.Restore(context.Background(), plan.Items[0], nil)
	if o.Err != nil || ResultOf(o) != ResultDone {
		t.Fatal(o.Error())
	}
	if !slices.Contains(r.calls(), "plugin install o/r --ref "+commitV1+" --yes") {
		t.Errorf("herdr was not asked for the exported commit: %q", r.calls())
	}
	plugins, _ := r.m.Installed(context.Background())
	if len(plugins) != 1 || plugins[0].Enabled || TrackingOf(plugins[0]).Kind != TrackRelease || TrackingOf(plugins[0]).Ref != "v1.0.0" {
		t.Errorf("after the restore: %+v, %+v", plugins, TrackingOf(plugins[0]))
	}
	entries, _ := r.m.HistoryEntries()
	if len(entries) != 1 || entries[0].Kind != KindRestore || entries[0].Failed() {
		t.Fatalf("history = %+v", entries)
	}
	if u, err := r.m.PlanRollback(context.Background(), "o.r"); err != nil || !u.Remove {
		t.Errorf("rolling back the restore: %+v, %v; want an uninstall", u, err)
	}
}

func TestRestoreChangesOnlyTheEnabledStateAndCanBeRolledBack(t *testing.T) {
	r, exp := planFixture(t, true)
	plan, err := r.m.PlanRestore(context.Background(), exp, []string{"o.toggle"}, "0.9.1")
	if err != nil {
		t.Fatal(err)
	}
	o := r.m.Restore(context.Background(), plan.Items[0], nil)
	if o.Err != nil {
		t.Fatal(o.Error())
	}
	if o.After == nil || o.After.Enabled || o.Before == nil || !o.Before.Enabled || o.Entry == "" {
		t.Errorf("before %+v, after %+v, entry %q", o.Before, o.After, o.Entry)
	}
	if r.installed() {
		t.Errorf("disabling reinstalled: %q", r.calls())
	}
	entries, _ := r.m.HistoryEntries()
	if len(entries) != 1 || entries[0].Kind != KindRestore || entries[0].Result() != "done" ||
		!entries[0].Before.Enabled || entries[0].After.Enabled || !entries[0].Before.SameRevision(*entries[0].After) {
		t.Fatalf("history = %+v", entries)
	}

	u, err := r.m.PlanRollback(context.Background(), "o.toggle")
	if err != nil || !u.EnabledOnly || u.Remove {
		t.Fatalf("undo = %+v, %v; want the enabled state changed back", u, err)
	}
	if !strings.Contains(u.Describe(), "undo the import of o.toggle") || !strings.HasSuffix(u.Describe(), ": enable it again") {
		t.Errorf("describe = %q", u.Describe())
	}
	o = r.m.Rollback(context.Background(), u, nil)
	if o.Err != nil || o.After == nil || !o.After.Enabled {
		t.Fatalf("rollback: %+v, %v", o.After, o.Error())
	}
	if p := r.plugins(); !slices.ContainsFunc(p, func(p herdr.InstalledPluginInfo) bool { return p.PluginID == "o.toggle" && p.Enabled }) || r.installed() {
		t.Errorf("after the rollback: %+v, calls %q", p, r.calls())
	}
	if _, err := r.m.PlanRollback(context.Background(), "o.toggle"); err != nil {
		t.Errorf("the rollback itself should be undoable: %v", err)
	}
}

func TestRestoreEnabledOnlyReportsFailureAndCancellation(t *testing.T) {
	t.Run("the self plugin cannot be disabled", func(t *testing.T) {
		r, exp := planFixture(t, true)
		plan, _ := r.m.PlanRestore(context.Background(), exp, []string{"o.toggle"}, "0.9.1")
		r.m.SelfID = "o.toggle"
		o := r.m.Restore(context.Background(), plan.Items[0], nil)
		if ResultOf(o) != ResultFailed || !errors.Is(o.Err, ErrSelf) {
			t.Errorf("outcome = %+v", o)
		}
		entries, _ := r.m.HistoryEntries()
		if len(entries) != 1 || entries[0].Result() != "failed" || !entries[0].After.Enabled {
			t.Errorf("history = %+v", entries)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		r, exp := planFixture(t, true)
		plan, _ := r.m.PlanRestore(context.Background(), exp, []string{"o.toggle"}, "0.9.1")
		o := r.m.Restore(canceledCtx(), plan.Items[0], nil)
		if ResultOf(o) != ResultCancelled {
			t.Errorf("outcome = %+v, %v", o, o.Err)
		}
		entries, _ := r.m.HistoryEntries()
		if len(entries) != 1 || entries[0].Result() != "cancelled" {
			t.Errorf("history = %+v", entries)
		}
	})
}

// otherSource is at(ref, commit, enabled) installed from o/other instead.
func otherSource(ref, commit string, enabled bool) herdr.InstalledPluginInfo {
	p := at(ref, commit, enabled)
	info := p.Source.ValueOrZero()
	info.Repo = herdr.Some("other")
	p.Source = herdr.Some(info)
	return p
}

func TestRestoreRefusesAPlanThePluginHasMovedOnFrom(t *testing.T) {
	enabled, disabled := true, false
	for _, tt := range []struct {
		name    string
		planned *herdr.InstalledPluginInfo
		now     []herdr.InstalledPluginInfo
		item    RestoreItem
	}{
		{
			// Installed from another source by another process after the
			// plan was shown.
			name: "a change whose source was replaced", planned: new(at("v1.0.0", commitV1, true)),
			now: []herdr.InstalledPluginInfo{otherSource("v1.0.0", commitV1, true)},
			item: RestoreItem{Action: RestoreChange, Plugin: ExportedPlugin{ID: "o.r", Enabled: &enabled},
				Target: Target{Source: src, Ref: "v2.0.0", Commit: commitV2, Enabled: &enabled}},
		},
		{
			name: "an install of a plugin installed since", planned: nil,
			now: []herdr.InstalledPluginInfo{otherSource("", commitV1, true)},
			item: RestoreItem{Action: RestoreInstall, Plugin: ExportedPlugin{ID: "o.r", Enabled: &enabled},
				Target: Target{Source: src, Ref: "v2.0.0", Commit: commitV2, Enabled: &enabled}},
		},
		{
			name: "a disable of a plugin reinstalled since", planned: new(at("v1.0.0", commitV1, true)),
			now:  []herdr.InstalledPluginInfo{otherSource("v1.0.0", commitV1, true)},
			item: RestoreItem{Action: RestoreDisable, Plugin: ExportedPlugin{ID: "o.r", Enabled: &disabled}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := newRegistry(t, true, "", tt.now...)
			r.installs(at(commitV2, commitV2, true))
			tt.item.Current = tt.planned
			o := r.m.Restore(context.Background(), tt.item, nil)
			if !errors.Is(o.Err, ErrChanged) || !strings.Contains(o.Err.Error(), "review it again") || !strings.Contains(o.Err.Error(), "is o/other ") {
				t.Fatalf("err = %v, want ErrChanged", o.Err)
			}
			if o.Before == nil || o.Before.Source != "o/other" || o.After == nil || o.After.Source != "o/other" || !o.After.Enabled {
				t.Errorf("the outcome does not say the plugin was left as it is now: before %+v, after %+v", o.Before, o.After)
			}
			if r.installed() {
				t.Errorf("herdr ran over the newer install: %q", r.calls())
			}
			if got := r.plugins(); len(got) != 1 || StateOf(got[0]).Source != "o/other" || !got[0].Enabled {
				t.Errorf("registry = %+v", got)
			}
			entries, _ := r.m.HistoryEntries()
			if len(entries) != 1 || entries[0].Result() != "failed" || !strings.Contains(entries[0].Error, "review it again") {
				t.Errorf("history = %+v", entries)
			}
		})
	}
}

func TestRestoreResults(t *testing.T) {
	for _, tt := range []struct {
		name string
		o    Outcome
		want RestoreResult
	}{
		{"done", Outcome{}, ResultDone},
		{"failed", Outcome{Err: ErrKeepDisabled}, ResultFailed},
		{"cancelled", Outcome{Err: cancelled(canceledCtx(), ErrKeepDisabled)}, ResultCancelled},
		{"unconfirmed", Outcome{AfterUnknown: true, Err: ErrUnconfirmed}, ResultUnconfirmed},
	} {
		if got := ResultOf(tt.o); got != tt.want {
			t.Errorf("%s: %s, want %s", tt.name, got, tt.want)
		}
	}
}

func canceledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
