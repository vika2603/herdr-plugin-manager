package manager

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-plugin-manager/internal/source"
)

// blockFollows makes every write of the kept refs fail.
func blockFollows(t *testing.T, h *History) {
	t.Helper()
	if err := os.MkdirAll(h.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(h.followsPath()+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateWhoseFollowCannotBeKeptFails(t *testing.T) {
	r := newRegistry(t, true, "", at("v1.0.0", commitV1, true))
	r.m.Git = releases
	r.installs(at(commitV2, commitV2, true))
	blockFollows(t, r.m.History)
	current := at("v1.0.0", commitV1, true)
	o := r.m.Apply(context.Background(), Change{Kind: KindUpdate, ID: "o.r", Current: &current,
		Target: Target{Source: src, Ref: "v2.0.0", Commit: commitV2}}, nil)
	if o.Err == nil {
		t.Fatal("an update left pinned instead of following v2.0.0 was reported as done")
	}
	if !strings.Contains(o.Err.Error(), "listed as pinned") {
		t.Errorf("the error does not say how the plugin is followed now: %v", o.Err)
	}
	if o.After == nil || o.After.Commit != commitV2 || TrackingAt(o.After.Ref, o.After.Commit).Kind != TrackPinned {
		t.Errorf("after = %+v, want the installed commit, pinned", o.After)
	}
	entries, err := r.m.HistoryEntries()
	if err != nil || len(entries) != 1 || !entries[0].Failed() {
		t.Errorf("history = %+v, %v; want one failed entry", entries, err)
	}
}

func TestPinWhoseOldFollowCannotBeForgottenFails(t *testing.T) {
	r := newRegistry(t, true, "", at(commitV2, commitV2, true))
	r.m.Git = releases
	if err := r.m.History.setFollow("o.r", &Follow{Source: src.String(), Ref: "v2.0.0", Commit: commitV2}); err != nil {
		t.Fatal(err)
	}
	blockFollows(t, r.m.History)
	r.installs(at(commitV2, commitV2, true))
	current := at("v2.0.0", commitV2, true)
	o := r.m.Apply(context.Background(), Change{Kind: KindPin, ID: "o.r", Current: &current,
		Target: Target{Source: src, Ref: commitV2, Commit: commitV2}}, nil)
	if o.Err == nil {
		t.Fatal("a pin still following v2.0.0 was reported as done")
	}
	if !strings.Contains(o.Err.Error(), "still listed as following") {
		t.Errorf("the error does not say how the plugin is followed now: %v", o.Err)
	}
	if o.After == nil || TrackingAt(o.After.Ref, o.After.Commit).Kind != TrackRelease {
		t.Errorf("after = %+v, want it still following the release", o.After)
	}
}

func TestFollowsOfSeparateProcessesAreAllKept(t *testing.T) {
	dir := t.TempDir()
	// Separate Histories share no mutex, as two processes do not.
	histories := []*History{{Dir: dir}, {Dir: dir}, {Dir: dir}}
	const each = 20
	var wg sync.WaitGroup
	errs := make(chan error, len(histories)*each)
	for i, h := range histories {
		wg.Go(func() {
			for j := range each {
				id := fmt.Sprintf("p%d.%d", i, j)
				errs <- h.setFollow(id, &Follow{Source: "o/" + id, Ref: "main", Commit: commitV1})
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	follows, err := histories[0].follows()
	if err != nil {
		t.Fatal(err)
	}
	if len(follows) != len(histories)*each {
		t.Errorf("kept %d follows, want %d", len(follows), len(histories)*each)
	}
}

func TestConcurrentUpdatesByTwoManagersKeepBothFollows(t *testing.T) {
	other := func(p herdr.InstalledPluginInfo) herdr.InstalledPluginInfo {
		p.PluginID = "o.s"
		info := p.Source.ValueOrZero()
		info.Repo = herdr.Some("s")
		p.Source = herdr.Some(info)
		return p
	}
	a, b := at("main", commitV1, true), other(at("main", commitV1, true))
	r1, r2 := newRegistry(t, true, "", a), newRegistry(t, true, "", b)
	shared := t.TempDir()
	r1.m.History, r2.m.History = &History{Dir: shared}, &History{Dir: shared}
	r1.installs(at(commitV2, commitV2, true))
	r2.installs(other(at(commitV2, commitV2, true)))
	for range 20 {
		_ = os.Remove(r1.m.History.followsPath())
		r1.write(r1.file, []herdr.InstalledPluginInfo{a})
		r2.write(r2.file, []herdr.InstalledPluginInfo{b})
		changes := []struct {
			m *Manager
			c Change
		}{
			{r1.m, Change{Kind: KindUpdate, ID: "o.r", Current: &a, Target: Target{Source: src, Ref: "main", Commit: commitV2}}},
			{r2.m, Change{Kind: KindUpdate, ID: "o.s", Current: &b, Target: Target{Source: source.GitHub{Owner: "o", Repo: "s"}, Ref: "main", Commit: commitV2}}},
		}
		outcomes := make([]Outcome, len(changes))
		var wg sync.WaitGroup
		for i, ch := range changes {
			wg.Go(func() { outcomes[i] = ch.m.Apply(context.Background(), ch.c, nil) })
		}
		wg.Wait()
		for _, o := range outcomes {
			if o.Err != nil {
				t.Fatalf("%s: %v", o.ID, o.Err)
			}
			if o.After == nil || o.After.Ref != "main" {
				t.Fatalf("%s after = %+v, want it following main", o.ID, o.After)
			}
		}
		follows, err := r1.m.History.follows()
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := follows["o.r"]; !ok {
			t.Fatalf("the follow of o.r was lost: %v", follows)
		}
		if _, ok := follows["o.s"]; !ok {
			t.Fatalf("the follow of o.s was lost: %v", follows)
		}
	}
}

func TestUninstallWhoseFollowCannotBeForgottenFails(t *testing.T) {
	r := newRegistry(t, true, "", at(commitV2, commitV2, true))
	if err := r.m.History.setFollow("o.r", &Follow{Source: src.String(), Ref: "v2.0.0", Commit: commitV2}); err != nil {
		t.Fatal(err)
	}
	blockFollows(t, r.m.History)
	r.installs()
	cli, _ := fakeHerdr(t, `case "$*" in
"plugin uninstall "*) cp `+r.next+` `+r.file+` ;;
esac`)
	r.m.CLI = cli
	err := r.m.Uninstall(context.Background(), "o.r", nil)
	if err == nil || !strings.Contains(err.Error(), "could not be forgotten") {
		t.Fatalf("err = %v, want the follow left behind reported", err)
	}
	if !strings.Contains(err.Error(), "no longer installed") {
		t.Errorf("the error does not say the plugin was removed: %v", err)
	}
}

// pinnedAt is o.r pinned to commit, installed at ms.
func pinnedAt(commit string, ms uint64) herdr.InstalledPluginInfo {
	p := at(commit, commit, true)
	info := p.Source.ValueOrZero()
	info.InstalledUnixMs = herdr.Some(ms)
	p.Source = herdr.Some(info)
	return p
}

func TestFollowIsOnlyOfTheInstallItWasKeptFor(t *testing.T) {
	follows := map[string]Follow{"o.r": {Source: src.String(), Ref: "v2.0.0", Commit: commitV2, Installed: 100}}
	list := []herdr.InstalledPluginInfo{pinnedAt(commitV2, 100)}
	applyFollows(list, follows)
	if k := TrackingOf(list[0]).Kind; k != TrackRelease {
		t.Errorf("the install the follow was kept for is %s, want it following the release", k)
	}
	// herdr installed it again at the same pin, outside this manager.
	list = []herdr.InstalledPluginInfo{pinnedAt(commitV2, 200)}
	applyFollows(list, follows)
	if k := TrackingOf(list[0]).Kind; k != TrackPinned {
		t.Errorf("a later install at the same pin is %s, want it pinned as herdr records", k)
	}
}

func TestUpdateKeepsTheInstallTimeOfItsFollow(t *testing.T) {
	r := newRegistry(t, true, "", at("v1.0.0", commitV1, true))
	r.m.Git = releases
	r.installs(pinnedAt(commitV2, 1234))
	current := at("v1.0.0", commitV1, true)
	o := r.m.Apply(context.Background(), Change{Kind: KindUpdate, ID: "o.r", Current: &current,
		Target: Target{Source: src, Ref: "v2.0.0", Commit: commitV2}}, nil)
	if o.Err != nil {
		t.Fatal(o.Err)
	}
	follows, err := r.m.History.follows()
	if err != nil {
		t.Fatal(err)
	}
	if f := follows["o.r"]; f.Installed != 1234 || f.Ref != "v2.0.0" {
		t.Errorf("follow = %+v, want v2.0.0 for the install at 1234", f)
	}
}
