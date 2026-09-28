package market

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const sampleIndex = `{
  "schemaVersion": 1,
  "generatedAt": "2026-09-28T06:30:56Z",
  "pluginCount": 3,
  "plugins": [
    {
      "fullName": "alice/tools", "owner": "alice", "name": "tools",
      "description": "Assorted tools", "stars": 5, "language": "Go",
      "topics": ["herdr-plugin", "git"],
      "createdAt": "2026-01-01T00:00:00Z", "pushedAt": "2026-09-01T00:00:00Z",
      "headCommit": "aaaa",
      "manifests": [
        {"path": "herdr-plugin.toml", "id": "alice.tools", "name": "Tools", "version": "1.0.0", "minHerdrVersion": "0.9.0"},
        {"path": "extras/notify/herdr-plugin.toml", "id": "alice.notify", "name": "Notify", "version": "0.1.0",
         "minHerdrVersion": "0.8.0", "description": "Desktop notifications"}
      ]
    },
    {
      "fullName": "bob/board", "owner": "bob", "name": "board",
      "description": "Agent board", "stars": 50, "language": "Rust",
      "createdAt": "2026-06-01T00:00:00Z", "pushedAt": "2026-08-01T00:00:00Z",
      "headCommit": "bbbb",
      "manifests": [{"path": "herdr-plugin.toml", "id": "bob.board", "name": "Board", "version": "2.0.0", "minHerdrVersion": "0.9.1"}]
    }
  ]
}`

func TestEntriesFlattensManifests(t *testing.T) {
	ix, err := decodeIndex([]byte(sampleIndex))
	if err != nil {
		t.Fatal(err)
	}
	entries := ix.Entries()
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	notify, ok := Find(entries, "alice.notify")
	if !ok {
		t.Fatal("alice.notify not found")
	}
	if notify.Source.String() != "alice/tools/extras/notify" {
		t.Errorf("source = %q", notify.Source)
	}
	if notify.Repo.HeadCommit != "aaaa" {
		t.Errorf("head commit = %q", notify.Repo.HeadCommit)
	}
	if _, ok := Find(entries, "BOB/board"); !ok {
		t.Error("lookup by source shorthand should ignore case")
	}
	tools, _ := Find(entries, "alice.tools")
	if tools.Description() != "Assorted tools" || notify.Description() != "Desktop notifications" {
		t.Errorf("descriptions = %q, %q", tools.Description(), notify.Description())
	}
}

func TestSort(t *testing.T) {
	ix, _ := decodeIndex([]byte(sampleIndex))
	entries := ix.Entries()
	Sort(entries, ByStars)
	if got := strings.Join(ids(entries), ","); got != "bob.board,alice.notify,alice.tools" {
		t.Errorf("by stars: %s", got)
	}
	Sort(entries, ByRecent)
	if got := strings.Join(ids(entries), ","); got != "alice.notify,alice.tools,bob.board" {
		t.Errorf("by recent: %s", got)
	}
	Sort(entries, ByName)
	if got := strings.Join(ids(entries), ","); got != "bob.board,alice.notify,alice.tools" {
		t.Errorf("by name: %s", got)
	}
}

func TestDecodeRejectsOtherSchemaVersion(t *testing.T) {
	if _, err := decodeIndex([]byte(`{"schemaVersion": 2, "plugins": []}`)); err == nil {
		t.Fatal("schema version 2 accepted")
	}
}

func TestLoadCachesAndFallsBack(t *testing.T) {
	var hits atomic.Int32
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		if fail.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(sampleIndex))
	}))
	defer server.Close()

	now := time.Now()
	c := NewClient(t.TempDir(), "test")
	c.IndexURL = server.URL
	c.Now = func() time.Time { return now }
	ctx := context.Background()

	if _, st, err := c.Load(ctx, false); err != nil || st.FromCache {
		t.Fatalf("first load: %+v, %v", st, err)
	}
	if _, st, err := c.Load(ctx, false); err != nil || !st.FromCache || hits.Load() != 1 {
		t.Fatalf("fresh cache not used: %+v, %v, hits=%d", st, err, hits.Load())
	}
	if _, _, err := c.Load(ctx, true); err != nil || hits.Load() != 2 {
		t.Fatalf("refresh did not download: %v, hits=%d", err, hits.Load())
	}

	fail.Store(true)
	ix, st, err := c.Load(ctx, true)
	if err != nil || ix == nil || !st.FromCache || st.FetchErr == nil {
		t.Fatalf("failed download should fall back to cache: %+v, %v", st, err)
	}

	if err := os.Remove(filepath.Join(c.CacheDir, cacheFile)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Load(ctx, false); err == nil {
		t.Fatal("failed download without cache should fail")
	}
}

func TestManifestValidates(t *testing.T) {
	var path atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path.Store(r.URL.Path)
		_, _ = w.Write([]byte(`id = "alice.tools"
name = "Tools"
version = "1.0.0"
min_herdr_version = "0.9.0"
platforms = ["linux", "macos"]

[[build]]
command = ["go", "build", "./..."]
`))
	}))
	defer server.Close()

	c := NewClient("", "test")
	c.HTTP = server.Client()
	// Route the raw.githubusercontent.com URL to the test server.
	c.HTTP.Transport = rewriteHost{target: server.URL, base: http.DefaultTransport}
	m, _, err := c.Manifest(context.Background(), mustSource(t, "alice/tools/sub"), "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "alice.tools" || len(m.Build) != 1 {
		t.Errorf("manifest = %+v", m)
	}
	if got := path.Load(); got != "/alice/tools/v1.0.0/sub/herdr-plugin.toml" {
		t.Errorf("requested path %q", got)
	}
}

func TestSearchRequiresEveryTerm(t *testing.T) {
	ix, _ := decodeIndex([]byte(sampleIndex))
	entries := ix.Entries()
	if got := Search(entries, "  ", ByRelevance); len(got) != 3 {
		t.Errorf("empty query kept %d entries", len(got))
	}
	got := Search(entries, "ALICE desktop", ByRelevance)
	if len(got) != 1 || got[0].Manifest.ID != "alice.notify" {
		t.Errorf("Search = %v", ids(got))
	}
	if got := Search(entries, "git", ByRelevance); len(got) != 2 {
		t.Errorf("topic match: %v", ids(got))
	}
}

func TestSearchRanksNameAboveDescriptionAboveTopic(t *testing.T) {
	entry := func(id, name, desc, repoDesc string, stars int, topics ...string) Entry {
		return Entry{
			Repo:     &Repository{Stars: stars, Description: repoDesc, Topics: topics},
			Manifest: Manifest{ID: id, Name: name, Description: desc},
		}
	}
	entries := []Entry{
		entry("a.topic-only", "Remote", "", "", 900, "ssh"),
		entry("b.repo-desc", "Mirror", "Mirror panes", "Mirror over SSH", 800),
		entry("c.desc", "Rename", "Renames tabs for ssh sessions", "", 500),
		entry("d.contains", "Openssh Tools", "", "", 1000),
		entry("e.word", "SSH Manager", "", "", 2),
		entry("x.ssh", "Sessions", "", "", 0),
	}
	got := strings.Join(ids(Search(entries, "ssh", ByRelevance)), ",")
	want := "x.ssh,e.word,d.contains,c.desc,a.topic-only,b.repo-desc"
	if got != want {
		t.Errorf("ranking:\n got %s\nwant %s", got, want)
	}
	if got := strings.Join(ids(Search(entries, "ssh", ByStars)), ","); !strings.HasPrefix(got, "d.contains,a.topic-only,b.repo-desc") {
		t.Errorf("an explicit order should override relevance: %s", got)
	}
}

func TestShownDescriptionAndTopics(t *testing.T) {
	e := Entry{
		Repo:     &Repository{Description: "Mirror panes over SSH", Topics: []string{"herdr-plugin", "ssh", "remote-ssh", "tmux"}},
		Manifest: Manifest{Description: "Mirror panes"},
	}
	terms := Terms("ssh")
	if got := ShownDescription(e, terms); got != "Mirror panes over SSH" {
		t.Errorf("ShownDescription = %q, want the repository description that matches", got)
	}
	if got := ShownDescription(e, Terms("panes")); got != "Mirror panes" {
		t.Errorf("ShownDescription = %q, want the manifest description", got)
	}
	if got := strings.Join(MatchedTopics(e, terms), ","); got != "ssh,remote-ssh" {
		t.Errorf("MatchedTopics = %q", got)
	}
}

func TestSnippetKeepsTheMatchInView(t *testing.T) {
	text := "Auto-name tabs after where the work is and what is running, including remote ssh sessions"
	got := Snippet(text, Terms("ssh"), 40)
	if !strings.Contains(got, "ssh") || !strings.HasPrefix(got, "…") || len([]rune(got)) > 40 {
		t.Errorf("Snippet = %q (%d runes)", got, len([]rune(got)))
	}
	if got := Snippet(text, Terms("tabs"), 20); got != "Auto-name tabs afte…" {
		t.Errorf("an early match should keep the start: %q", got)
	}
	if got := Snippet("short", Terms("x"), 20); got != "short" {
		t.Errorf("short text changed: %q", got)
	}
}

func TestDecodeIndexSanitizesText(t *testing.T) {
	data := strings.Replace(sampleIndex, `"Assorted tools"`, `"Assorted\u001b]52;c;cGF5bG9hZA==\u0007 tools"`, 1)
	ix, err := decodeIndex([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if d := ix.Repositories[0].Description; strings.ContainsAny(d, "\x1b\x07") {
		t.Fatalf("repository description keeps raw controls: %q", d)
	}
}
