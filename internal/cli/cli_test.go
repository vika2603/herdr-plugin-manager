package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-plugin-manager/internal/herdrcli"
	"github.com/vika2603/herdr-plugin-manager/internal/manager"
	"github.com/vika2603/herdr-plugin-manager/internal/market"
	"github.com/vika2603/herdr-plugin-manager/internal/updates"
)

var (
	head    = strings.Repeat("b", 40)
	older   = strings.Repeat("a", 40)
	noneYet = `{"result":{"type":"plugin_list","plugins":[]}}`
)

const index = `{"schemaVersion": 1, "plugins": [{
  "fullName": "carol/gadget", "owner": "carol", "name": "gadget", "stars": 3,
  "headCommit": "` + "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" + `",
  "manifests": [{"path": "herdr-plugin.toml", "id": "carol.gadget", "name": "Gadget", "version": "0.1.0"}]
}]}`

// harness runs commands against a manager whose herdr is a shell script,
// whose GitHub and marketplace are a test server, and whose remotes all
// point at head.
type harness struct {
	t        *testing.T
	m        *manager.Manager
	calls    func() []string
	listFile string
	// manifestIDs maps "owner/repo" to the id its manifest declares.
	manifestIDs map[string]string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the herdr binary")
	}
	h := &harness{t: t, manifestIDs: map[string]string{"carol/gadget": "carol.gadget"}}
	dir := t.TempDir()
	callsFile := filepath.Join(dir, "calls.txt")
	h.listFile = filepath.Join(dir, "list.json")
	h.setInstalled(noneYet)
	script := "#!/bin/sh\necho \"$*\" >> " + callsFile + "\ncase \"$*\" in\n" +
		"--version) echo 'herdr 0.9.1' ;;\n" +
		"'plugin list --json') cat " + h.listFile + " ;;\nesac\n"
	bin := filepath.Join(dir, "herdr")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	h.calls = func() []string {
		data, _ := os.ReadFile(callsFile)
		return strings.Split(strings.TrimSpace(string(data)), "\n")
	}

	server := httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(server.Close)
	mc := market.NewClient(filepath.Join(dir, "cache"), "test")
	mc.HTTP = &http.Client{Transport: redirect{server.URL}}
	h.m = &manager.Manager{
		API:    herdr.New(filepath.Join(dir, "missing.sock")),
		CLI:    herdrcli.Runner{Bin: bin},
		Market: mc,
		Git:    lister{},
	}
	return h
}

func (h *harness) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/plugins/index.json" {
		_, _ = w.Write([]byte(index))
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) == 4 && parts[3] == "herdr-plugin.toml" && parts[2] == head {
		if id, ok := h.manifestIDs[parts[0]+"/"+parts[1]]; ok {
			_, _ = w.Write([]byte("id = \"" + id + "\"\nname = \"Gadget\"\nversion = \"0.2.0\"\nmin_herdr_version = \"0.9.0\"\n" +
				"[[build]]\ncommand = [\"make\"]\n"))
			return
		}
	}
	http.NotFound(w, r)
}

func (h *harness) setInstalled(listJSON string) {
	if err := os.WriteFile(h.listFile, []byte(listJSON), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

// run executes one command line. interactive answers prompts from stdin.
func (h *harness) run(stdin string, interactive bool, args ...string) (stdout, stderr string, err error) {
	var out, errOut bytes.Buffer
	c := &cli{m: h.m, in: strings.NewReader(stdin), out: &out, errOut: &errOut, interactive: interactive}
	root := c.root(nil)
	root.SetArgs(args)
	err = root.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

func (h *harness) ran(prefix string) bool {
	for _, c := range h.calls() {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// lister reports every remote with its default branch and tags at head.
type lister struct{ tags []string }

func (l lister) List(context.Context, string) (updates.Refs, error) {
	tags := map[string]string{}
	for _, t := range l.tags {
		tags[t] = head
	}
	return updates.Refs{Head: head, HeadBranch: "main", Branches: map[string]string{"main": head}, Tags: tags}, nil
}

type redirect struct{ target string }

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	out := req.Clone(req.Context())
	out.URL.Scheme = "http"
	out.URL.Host = strings.TrimPrefix(r.target, "http://")
	return http.DefaultTransport.RoundTrip(out)
}

func TestInstallAsksFirst(t *testing.T) {
	h := newHarness(t)
	out, _, err := h.run("", false, "install", "carol.gadget")
	if !errors.Is(err, errNeedYes) {
		t.Fatalf("without a terminal or --yes: err = %v, want errNeedYes", err)
	}
	if !strings.Contains(out, "Build commands") || !strings.Contains(out, "make") {
		t.Errorf("the preview was not shown before asking:\n%s", out)
	}
	if _, _, err := h.run("n\n", true, "install", "carol.gadget"); !errors.Is(err, errCancelled) {
		t.Errorf("answering n: err = %v, want errCancelled", err)
	}
	if h.ran("plugin install") {
		t.Fatalf("installed without confirmation: %q", h.calls())
	}

	if _, _, err := h.run("", false, "install", "carol.gadget", "--yes"); err != nil {
		t.Fatal(err)
	}
	if !h.ran("plugin install carol/gadget --yes") {
		t.Errorf("herdr was not asked to install: %q", h.calls())
	}
}

func TestInstallTakesTheReleaseThePreviewShowed(t *testing.T) {
	h := newHarness(t)
	h.m.Git = lister{tags: []string{"v0.1.0", "v0.2.0"}}
	out, _, err := h.run("", false, "install", "carol.gadget", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "@ v0.2.0") {
		t.Errorf("the preview does not name the release:\n%s", out)
	}
	if !h.ran("plugin install carol/gadget --ref v0.2.0 --yes") {
		t.Errorf("herdr was not asked for the release: %q", h.calls())
	}
}

func TestResolveTakesTheHeadCommitFromTheIndex(t *testing.T) {
	h := newHarness(t)
	c := &cli{m: h.m, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}}
	ctx := context.Background()
	src, hint, err := c.resolve(ctx, "carol.gadget", "")
	if err != nil || src.String() != "carol/gadget" || hint != head {
		t.Errorf("by id: %v %q %v; want carol/gadget with the index head", src, hint, err)
	}
	// The id lookup cached the index, so a source finds its listing too.
	if _, hint, _ := c.resolve(ctx, "carol/gadget", ""); hint != head {
		t.Errorf("by source: hint %q, want the index head", hint)
	}
	if _, hint, _ := c.resolve(ctx, "carol/gadget", "dev"); hint != "" {
		t.Errorf("with a ref: hint %q; the index only knows the default branch", hint)
	}
	if src, hint, err := c.resolve(ctx, "dave/tool", ""); err != nil || src.String() != "dave/tool" || hint != "" {
		t.Errorf("an unlisted source: %v %q %v", src, hint, err)
	}
	if _, _, err := c.resolve(ctx, "dave.tool", ""); err == nil {
		t.Error("an unknown id should fail")
	}
}

func TestSearch(t *testing.T) {
	h := newHarness(t)
	if _, _, err := h.run("", false, "search", "--sort", "stars"); err == nil || !strings.Contains(err.Error(), "trending") {
		t.Errorf("an unknown order should list the valid ones: %v", err)
	}
	h.setInstalled(`{"result":{"type":"plugin_list","plugins":[{"plugin_id":"carol.gadget","name":"Gadget","version":"0.1.0","enabled":true,"manifest_path":"/x","plugin_root":"/x"}]}}`)
	out, _, err := h.run("", false, "search", "gadget", "--sort", "trending", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var results []searchResult
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(results) != 1 || results[0].ID != "carol.gadget" || !results[0].Installed {
		t.Errorf("results = %+v", results)
	}
}

func TestUpdateSkipsAPluginWhoseIDChanged(t *testing.T) {
	h := newHarness(t)
	h.manifestIDs["o/r"] = "someone.else"
	h.setInstalled(`{"result":{"type":"plugin_list","plugins":[{"plugin_id":"o.r","name":"R","version":"1","enabled":true,` +
		`"manifest_path":"/x","plugin_root":"/x","source":{"kind":"github","owner":"o","repo":"r","resolved_commit":"` + older + `"}}]}}`)
	_, stderr, err := h.run("", false, "update", "--yes")
	if err == nil || !strings.Contains(err.Error(), "not updated: o.r") {
		t.Errorf("err = %v, want o.r reported as not updated", err)
	}
	if !strings.Contains(stderr, "skipping o.r") {
		t.Errorf("stderr lacks the reason:\n%s", stderr)
	}
	if h.ran("plugin install") {
		t.Errorf("reinstalled a plugin whose id changed: %q", h.calls())
	}
}
