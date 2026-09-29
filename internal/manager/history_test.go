package manager

import (
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"
)

// sameMoment is the time every entry of the concurrency tests is recorded
// at, so that their ids can only differ by what reserve does.
var sameMoment = time.UnixMilli(1700000000123)

// TestHistoryChild records one entry at sameMoment when run as the child
// process of TestEntriesOfSeparateProcessesHaveTheirOwnIDs.
func TestHistoryChild(t *testing.T) {
	dir := os.Getenv("HPM_HISTORY_CHILD_DIR")
	if dir == "" {
		t.Skip("run by TestEntriesOfSeparateProcessesHaveTheirOwnIDs")
	}
	h := &History{Dir: dir}
	e := Entry{Time: sameMoment, Kind: KindUpdate, Plugin: os.Getenv("HPM_HISTORY_CHILD_PLUGIN")}
	if err := h.add(&e); err != nil {
		t.Fatal(err)
	}
}

func TestEntriesOfSeparateProcessesHaveTheirOwnIDs(t *testing.T) {
	dir := t.TempDir()
	plugins := []string{"alpha", "beta", "gamma"}
	var wg sync.WaitGroup
	for _, id := range plugins {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestHistoryChild$")
		cmd.Env = append(os.Environ(), "HPM_HISTORY_CHILD_DIR="+dir, "HPM_HISTORY_CHILD_PLUGIN="+id)
		wg.Go(func() {
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("child %s: %v\n%s", id, err, out)
			}
		})
	}
	wg.Wait()
	checkOwnIDs(t, &History{Dir: dir}, plugins)
}

func TestEntriesOfOneMomentHaveTheirOwnIDs(t *testing.T) {
	dir := t.TempDir()
	plugins := []string{"a", "b", "c", "d"}
	var wg sync.WaitGroup
	for _, id := range plugins {
		// Separate Histories, as separate processes would have.
		h := &History{Dir: dir}
		wg.Go(func() {
			e := Entry{Time: sameMoment, Kind: KindUpdate, Plugin: id}
			if err := h.add(&e); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	checkOwnIDs(t, &History{Dir: dir}, plugins)
}

// checkOwnIDs checks that h holds one entry for each plugin, each found by
// its own id with its own log file.
func checkOwnIDs(t *testing.T, h *History, plugins []string) {
	t.Helper()
	entries, err := h.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(plugins) {
		t.Fatalf("got %d entries, want %d: %+v", len(entries), len(plugins), entries)
	}
	logs := map[string]bool{}
	for _, e := range entries {
		got, ok, err := h.Find(e.ID)
		if err != nil || !ok || got.Plugin != e.Plugin {
			t.Errorf("Find(%s) = %s, %v, %v; want the entry of %s", e.ID, got.Plugin, ok, err, e.Plugin)
		}
		if e.Log == "" || logs[e.Log] {
			t.Errorf("entry %s of %s has log %q, not one of its own", e.ID, e.Plugin, e.Log)
		}
		logs[e.Log] = true
	}
}
