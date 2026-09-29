package manager

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/vika2603/herdr-client/herdr"
	"github.com/vika2603/herdr-client/herdrtest"

	"github.com/vika2603/herdr-plugin-manager/internal/herdrcli"
	"github.com/vika2603/herdr-plugin-manager/internal/market"
	"github.com/vika2603/herdr-plugin-manager/internal/source"
	"github.com/vika2603/herdr-plugin-manager/internal/updates"
)

// fakeHerdr is a shell script standing in for the herdr command. Every
// invocation appends its arguments as one line to calls.txt.
func fakeHerdr(t *testing.T, body string) (r herdrcli.Runner, calls func() []string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the herdr binary")
	}
	dir := t.TempDir()
	callsFile := filepath.Join(dir, "calls.txt")
	script := "#!/bin/sh\necho \"$*\" >> " + callsFile + "\n" + body + "\n"
	bin := filepath.Join(dir, "herdr")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return herdrcli.Runner{Bin: bin}, func() []string {
		data, _ := os.ReadFile(callsFile)
		return strings.Split(strings.TrimSpace(string(data)), "\n")
	}
}

// fakeLister reports fixed refs for every repository.
type fakeLister struct{ refs updates.Refs }

func (f fakeLister) List(context.Context, string) (updates.Refs, error) { return f.refs, nil }

// headAt is a lister whose default branch points at commit.
func headAt(commit string) fakeLister {
	return fakeLister{updates.Refs{Head: commit, Branches: map[string]string{}, Tags: map[string]string{"v2.0.0": commit}}}
}

func TestInstalledFallsBackToCommandWhenServerIsDown(t *testing.T) {
	cli, calls := fakeHerdr(t, `echo '{"result":{"type":"plugin_list","plugins":[{"plugin_id":"b","name":"B","version":"1","enabled":true,"manifest_path":"/b","plugin_root":"/b"},{"plugin_id":"a","name":"A","version":"1","enabled":true,"manifest_path":"/a","plugin_root":"/a"}]}}'`)
	m := &Manager{
		API: herdr.New(filepath.Join(t.TempDir(), "missing.sock")),
		CLI: cli,
	}
	plugins, err := m.Installed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 2 || plugins[0].PluginID != "a" {
		t.Fatalf("plugins = %+v", plugins)
	}
	if got := calls(); !slices.Equal(got, []string{"plugin list --json"}) {
		t.Errorf("calls = %q", got)
	}
}

func TestInstalledPrefersServer(t *testing.T) {
	cli, calls := fakeHerdr(t, `exit 1`)
	server := herdrtest.NewServer(t).Reply(herdr.MethodPluginList, herdr.PluginListResponse{
		Plugins: []herdr.InstalledPluginInfo{{PluginID: "live"}},
	})
	m := &Manager{API: server.Client(), CLI: cli}
	plugins, err := m.Installed(context.Background())
	if err != nil || len(plugins) != 1 || plugins[0].PluginID != "live" {
		t.Fatalf("plugins = %+v, %v", plugins, err)
	}
	if got := calls(); got[0] != "" {
		t.Errorf("herdr command called while the server answered: %q", got)
	}
}

func TestSelfProtection(t *testing.T) {
	cli, calls := fakeHerdr(t, `exit 0`)
	m := &Manager{CLI: cli, SelfID: "me"}
	if err := m.Uninstall(context.Background(), "me", nil); !errors.Is(err, ErrSelf) {
		t.Errorf("uninstall self: %v", err)
	}
	if err := m.SetEnabled(context.Background(), "me", false); !errors.Is(err, ErrSelf) {
		t.Errorf("disable self: %v", err)
	}
	if got := calls(); got[0] != "" {
		t.Errorf("herdr command called: %q", got)
	}
}

func TestSetEnabledWithoutServer(t *testing.T) {
	m := &Manager{API: herdr.New(filepath.Join(t.TempDir(), "missing.sock"))}
	err := m.SetEnabled(context.Background(), "x", true)
	if !IsServerDown(err) || !errors.Is(err, ErrServerNotRunning) {
		t.Fatalf("err = %v", err)
	}
}

func TestPreview(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`id = "o.r"
name = "R"
version = "2.0.0"
min_herdr_version = "0.10.0"
platforms = ["linux"]

[[build]]
command = ["sh", "-c", "make all"]

[[startup]]
command = ["./bin/r"]
`))
	}))
	defer server.Close()
	mc := market.NewClient("", "test")
	mc.HTTP = &http.Client{Transport: redirect{server.URL}}
	m := &Manager{Market: mc, Platform: "macos", Git: headAt(strings.Repeat("a", 40))}

	local := herdr.InstalledPluginInfo{PluginID: "o.r", Version: "1.0.0", PluginRoot: "/src/r",
		Source: herdr.Some(herdr.PluginSourceInfo{Kind: herdr.Some(herdr.PluginSourceKindLocal)})}
	p, err := m.Preview(context.Background(), source.GitHub{Owner: "o", Repo: "r"}, "", "", "0.9.1", []herdr.InstalledPluginInfo{local})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Problems) != 3 {
		t.Errorf("problems = %q, want platform, version and local conflict", p.Problems)
	}
	if p.Existing == nil || p.Existing.PluginID != "o.r" {
		t.Errorf("existing = %+v", p.Existing)
	}
	var text strings.Builder
	for _, s := range p.Sections() {
		fmt.Fprintf(&text, "%s\n%s\n", s.Title, strings.Join(s.Lines, "\n"))
	}
	for _, want := range []string{`sh -c "make all"`, "Startup commands", "./bin/r", "replaces installed 1.0.0 from local:/src/r"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("preview lacks %q:\n%s", want, text.String())
		}
	}
}

type redirect struct{ target string }

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	out := req.Clone(req.Context())
	out.URL.Scheme = "http"
	out.URL.Host = strings.TrimPrefix(r.target, "http://")
	return http.DefaultTransport.RoundTrip(out)
}

func TestPreviewEscapesTerminalControls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("id = \"o.r\"\nname = \"R\"\nversion = \"1.0.0\"\nmin_herdr_version = \"0.9.0\"\n" +
			"description = \"fine\\u001b[6A\\u001b[J\"\n" +
			"[[build]]\ncommand = [\"sh\", \"evil.sh\", \"\\r    make build\"]\n" +
			"[[panes]]\nid = \"p\"\ntitle = \"x\\u001b[6A\\u001b[J  Build commands\"\ncommand = [\"./p\"]\n"))
	}))
	defer server.Close()
	mc := market.NewClient("", "test")
	mc.HTTP = &http.Client{Transport: redirect{server.URL}}
	m := &Manager{Market: mc, Platform: "macos", Git: headAt(strings.Repeat("a", 40))}

	p, err := m.Preview(context.Background(), source.GitHub{Owner: "o", Repo: "r"}, "", "", "0.9.1", nil)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, s := range p.Sections() {
		for _, line := range s.Lines {
			text.WriteString(line + "\n")
		}
	}
	if strings.ContainsAny(text.String(), "\x1b\r") {
		t.Fatalf("preview carries raw terminal controls: %q", text.String())
	}
	if !strings.Contains(text.String(), `sh evil.sh "\r    make build"`) {
		t.Errorf("the carriage return should be visible in the quoted argument:\n%s", text.String())
	}
}

func TestCommandQuotesUnprintable(t *testing.T) {
	if got := Command([]string{"go", "build", "./..."}); got != "go build ./..." {
		t.Errorf("plain argv changed: %q", got)
	}
	nbsp := string(rune(0xa0))
	if got := Command([]string{"rm" + nbsp + "-rf"}); !strings.Contains(got, `\`+"u00a0") {
		t.Errorf("a no-break space should be escaped: %q", got)
	}
}

func TestPreviewReadsTheResolvedCommit(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte("id = \"o.r\"\nname = \"R\"\nversion = \"1.0.0\"\nmin_herdr_version = \"0.9.0\"\n"))
	}))
	defer server.Close()
	mc := market.NewClient("", "test")
	mc.HTTP = &http.Client{Transport: redirect{server.URL}}
	commit := strings.Repeat("c", 40)
	m := &Manager{Market: mc, Git: headAt(commit)}
	p, err := m.Preview(context.Background(), source.GitHub{Owner: "o", Repo: "r"}, "", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Commit != commit || !strings.Contains(path, "/"+commit+"/") {
		t.Errorf("preview commit %q read from %q, want the default branch head %s", p.Commit, path, commit)
	}
}

func TestPreviewUsesTheHintOnlyWhenRefResolvesToIt(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		_, _ = w.Write([]byte("id = \"o.r\"\nname = \"R\"\nversion = \"1.0.0\"\nmin_herdr_version = \"0.9.0\"\n"))
	}))
	defer server.Close()
	mc := market.NewClient("", "test")
	mc.HTTP = &http.Client{Transport: redirect{server.URL}}
	head, stale := strings.Repeat("c", 40), strings.Repeat("d", 40)
	m := &Manager{Market: mc, Git: headAt(head)}
	src := source.GitHub{Owner: "o", Repo: "r"}

	p, err := m.Preview(context.Background(), src, "", head, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Commit != head || len(paths) != 1 || !strings.Contains(paths[0], "/"+head+"/") {
		t.Errorf("right hint: commit %q, requests %q; want one read at %s", p.Commit, paths, head)
	}

	mu.Lock()
	paths = nil
	mu.Unlock()
	p, err = m.Preview(context.Background(), src, "", stale, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if p.Commit != head || !slices.ContainsFunc(paths, func(s string) bool { return strings.Contains(s, "/"+head+"/") }) {
		t.Errorf("stale hint: commit %q, requests %q; want the manifest read at %s", p.Commit, paths, head)
	}
}

// dirLister is a lister that also compares directories, reporting same for
// every one and counting the comparisons.
type dirLister struct {
	fakeLister
	same     bool
	compared *atomic.Int32
}

func (d dirLister) SameDir(context.Context, string, string, string, string) (bool, error) {
	d.compared.Add(1)
	return d.same, nil
}

func TestCheckIgnoresCommitsOutsideTheSubdirectory(t *testing.T) {
	installed := func(subdir string) herdr.InstalledPluginInfo {
		return herdr.InstalledPluginInfo{PluginID: "o.r", Source: herdr.Some(herdr.PluginSourceInfo{
			Kind: herdr.Some(herdr.PluginSourceKindGithub), Owner: herdr.Some("o"), Repo: herdr.Some("r"),
			Subdir: herdr.Some(subdir), ResolvedCommit: herdr.Some(strings.Repeat("a", 40)),
		})}
	}
	var compared atomic.Int32
	lister := func(same bool) dirLister {
		return dirLister{fakeLister: headAt(strings.Repeat("b", 40)), same: same, compared: &compared}
	}

	m := &Manager{Git: lister(true)}
	if res, err := m.Check(context.Background(), installed("plugins/herdr")); err != nil || res.Kind != updates.UpToDate {
		t.Errorf("commits outside the plugin: %v, %v; want up to date", res.Kind, err)
	}
	m = &Manager{Git: lister(false)}
	if res, err := m.Check(context.Background(), installed("plugins/herdr")); err != nil || res.Kind != updates.Available {
		t.Errorf("a change inside the plugin: %v, %v; want an update", res.Kind, err)
	}
	compared.Store(0)
	m = &Manager{Git: lister(true)}
	if res, _ := m.Check(context.Background(), installed("")); res.Kind != updates.Available || compared.Load() != 0 {
		t.Errorf("a plugin at the root: %v after %d comparisons; want an update without comparing", res.Kind, compared.Load())
	}
}

func TestPreviewPicksTheLatestReleaseAtTheRoot(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		_, _ = w.Write([]byte("id = \"o.r\"\nname = \"R\"\nversion = \"1.0.0\"\nmin_herdr_version = \"0.9.0\"\n"))
	}))
	defer server.Close()
	mc := market.NewClient("", "test")
	mc.HTTP = &http.Client{Transport: redirect{server.URL}}
	head, v1, v2 := strings.Repeat("h", 40), strings.Repeat("1", 40), strings.Repeat("2", 40)
	m := &Manager{Market: mc, Git: fakeLister{updates.Refs{Head: head, HeadBranch: "main",
		Branches: map[string]string{"main": head}, Tags: map[string]string{"v1.0.0": v1, "v2.0.0-rc.1": v2}}}}
	ctx := context.Background()

	p, err := m.Preview(ctx, source.GitHub{Owner: "o", Repo: "r"}, "", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Ref != "v1.0.0" || p.Commit != v1 || p.DefaultBranch != "main" || !slices.Equal(p.Releases, []string{"v2.0.0-rc.1", "v1.0.0"}) {
		t.Errorf("root plugin: ref %q commit %.7s branch %q releases %q; want v1.0.0, skipping the pre-release", p.Ref, p.Commit, p.DefaultBranch, p.Releases)
	}
	if last := paths[len(paths)-1]; !strings.Contains(last, "/"+v1+"/") {
		t.Errorf("manifest read from %q, want the release commit", last)
	}

	p, err = m.Preview(ctx, source.GitHub{Owner: "o", Repo: "r", Subdir: "plugin"}, "", "", "", nil)
	if err != nil || p.Ref != "" || p.Commit != head {
		t.Errorf("subdirectory plugin: ref %q commit %.7s %v; want the default branch", p.Ref, p.Commit, err)
	}
	p, err = m.Preview(ctx, source.GitHub{Owner: "o", Repo: "r"}, "main", "", "", nil)
	if err != nil || p.Ref != "main" || p.Commit != head {
		t.Errorf("explicit branch: ref %q commit %.7s %v", p.Ref, p.Commit, err)
	}
}

func TestRemoteReadmeFallsBackToTheRepositoryRoot(t *testing.T) {
	var asked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		if r.URL.Path == "/o/r/c1/README.md" {
			_, _ = w.Write([]byte("# Root\x1b[31m readme"))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	mc := market.NewClient("", "test")
	mc.HTTP = &http.Client{Transport: redirect{server.URL}}
	m := &Manager{Market: mc}
	doc, err := m.RemoteReadme(context.Background(), source.GitHub{Owner: "o", Repo: "r", Subdir: "plugins/x"}, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if asked[0] != "/o/r/c1/plugins/x/README.md" {
		t.Errorf("the plugin directory should be tried first, asked %q", asked)
	}
	if strings.Contains(doc.Markdown, "\x1b") || !strings.HasPrefix(doc.Markdown, "# Root") {
		t.Errorf("markdown = %q", doc.Markdown)
	}

	server.Config.Handler = http.NotFoundHandler()
	if _, err := m.RemoteReadme(context.Background(), source.GitHub{Owner: "o", Repo: "r"}, "c1"); !errors.Is(err, ErrNoReadme) {
		t.Errorf("err = %v, want ErrNoReadme", err)
	}
}

func TestInstalledReadme(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# Local"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &Manager{}
	doc, err := m.InstalledReadme(herdr.InstalledPluginInfo{PluginRoot: dir})
	if err != nil || doc.Markdown != "# Local" {
		t.Fatalf("doc = %+v, %v", doc, err)
	}
	if _, err := m.InstalledReadme(herdr.InstalledPluginInfo{PluginRoot: t.TempDir()}); !errors.Is(err, ErrNoReadme) {
		t.Errorf("err = %v, want ErrNoReadme", err)
	}
}
