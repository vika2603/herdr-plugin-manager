package manager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vika2603/herdr-client/herdr"
	"github.com/vika2603/herdr-client/plugin/manifest"

	"github.com/vika2603/herdr-plugin-manager/internal/market"
)

func TestHeadline(t *testing.T) {
	for _, tt := range []struct {
		from          State
		ref, commit   string
		version, want string
	}{
		{State{Version: "1.0.0", Ref: "v1.0.0", Commit: commitV1}, "v2.0.0", commitV2, "2.0.0", "New release v2.0.0, replacing v1.0.0"},
		{State{Version: "2.0.0", Ref: "v2.0.0", Commit: commitV2}, "v1.0.0", commitV1, "1.0.0", "Older release v1.0.0, replacing v2.0.0"},
		{State{Version: "1.0.0", Ref: "v1.0.0", Commit: commitV1}, "v2.0.0", commitV2, "1.9.0", "New release v2.0.0, replacing v1.0.0; version 1.0.0 -> 1.9.0"},
		{State{Version: "0.5.0", Commit: commitV1}, "", commitV2, "0.5.0", "New commits on the default branch; the version number stays 0.5.0"},
		{State{Version: "0.5.0", Ref: "main", Commit: commitV1}, "main", commitV2, "0.6.0", "New commits on main; version 0.5.0 -> 0.6.0"},
		{State{Version: "1.0.0", Ref: "v1.0.0", Commit: commitV1}, "main", commitV2, "1.1.0", "Switch from v1.0.0 to main; version 1.0.0 -> 1.1.0"},
		{State{Version: "1.0.0", Commit: commitV1}, commitV1, commitV1, "1.0.0", "Same commit, now followed as pinned to commit 111111111111; no updates until it is unpinned"},
	} {
		if got := headline(tt.from, tt.ref, tt.commit, tt.version); got != tt.want {
			t.Errorf("%s -> %s: %q, want %q", tt.from, tt.ref, got, tt.want)
		}
	}
}

func explainServer(t *testing.T, releases, compare string) *Manager {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases") && releases != "":
			_, _ = w.Write([]byte(releases))
		case strings.Contains(r.URL.Path, "/compare/") && compare != "":
			_, _ = w.Write([]byte(compare))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	mc := market.NewClient("", "test")
	mc.HTTP = &http.Client{Transport: redirect{server.URL}}
	return &Manager{Market: mc}
}

func previewOf(ref, commit, version string, build ...string) *Preview {
	mf := &manifest.Manifest{ID: "o.r", Version: version, MinHerdrVersion: "0.9.0"}
	for _, b := range build {
		mf.Build = append(mf.Build, manifest.Build{Command: strings.Fields(b)})
	}
	return &Preview{Source: src, Ref: ref, Commit: commit, Manifest: mf}
}

func TestExplainAReleaseWithItsNotes(t *testing.T) {
	m := explainServer(t, `[
	  {"tag_name": "v2.0.0", "name": "Two", "body": "Faster titles."},
	  {"tag_name": "v1.5.0", "body": "Fixes."},
	  {"tag_name": "v1.0.0", "body": "First."}
	]`, "")
	p := at("v1.0.0", commitV1, true)
	p.MinHerdrVersion = herdr.Some("0.8.0")
	p.Build = herdr.Some([]herdr.PluginManifestBuild{{Command: []string{"make"}}})
	e := m.Explain(context.Background(), p, previewOf("v2.0.0", commitV2, "2.0.0", "make", "go build"))
	if e.Headline != "New release v2.0.0, replacing v1.0.0" || e.From != "1.0.0 at v1.0.0 (111111111111)" || e.To != "2.0.0 at v2.0.0 (222222222222)" {
		t.Errorf("explanation = %+v", e)
	}
	if len(e.Releases) != 2 || e.Releases[0].Tag != "v2.0.0" || e.Releases[1].Tag != "v1.5.0" {
		t.Errorf("releases = %+v, want v2.0.0 and v1.5.0", e.Releases)
	}
	if strings.Join(e.Runs, "; ") != "min herdr: 0.8.0 -> 0.9.0; + build: go build" {
		t.Errorf("runs = %q", e.Runs)
	}
}

func TestExplainNewCommitsOnABranch(t *testing.T) {
	m := explainServer(t, "", `{"status": "ahead", "ahead_by": 1, "commits": [
	  {"sha": "c", "commit": {"message": "Fix the tab title", "author": {"name": "Ann"}}}
	]}`)
	p := at("", commitV1, true)
	p.MinHerdrVersion = herdr.Some("0.9.0")
	e := m.Explain(context.Background(), p, previewOf("", commitV2, "1.0.0"))
	if e.Headline != "New commits on the default branch; the version number stays 1.0.0" {
		t.Errorf("headline = %q", e.Headline)
	}
	if e.Commits == nil || strings.Join(e.CommitLines(20), "; ") != "Fix the tab title (Ann)" || e.NotesErr != nil {
		t.Errorf("commits = %+v, %v", e.Commits, e.NotesErr)
	}
	if len(e.Runs) != 0 {
		t.Errorf("runs = %q, want no change", e.Runs)
	}
}

func TestExplainSaysWhenNothingCanBeRead(t *testing.T) {
	m := explainServer(t, "", "")
	e := m.Explain(context.Background(), at("", commitV1, true), previewOf("", commitV2, "1.0.0"))
	if e.NotesErr == nil || e.Commits != nil || len(e.Releases) != 0 {
		t.Errorf("explanation = %+v, want the notes unknown", e)
	}
	var text strings.Builder
	for _, s := range e.Sections() {
		text.WriteString(s.Title + ": " + strings.Join(s.Lines, " / ") + "\n")
	}
	if !strings.Contains(text.String(), "What changed: not known: ") {
		t.Errorf("sections:\n%s", text.String())
	}
}
