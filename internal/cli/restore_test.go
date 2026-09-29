package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vika2603/herdr-plugin-manager/internal/manager"
)

// gadgetAndLink is carol.gadget on the default branch at head, and a
// plugin linked from a local directory.
const gadgetAndLink = `{"result":{"type":"plugin_list","plugins":[` +
	`{"plugin_id":"carol.gadget","name":"Gadget","version":"0.2.0","enabled":true,"manifest_path":"/x","plugin_root":"/x",` +
	`"source":{"kind":"github","owner":"carol","repo":"gadget","resolved_commit":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}},` +
	`{"plugin_id":"me.dev","name":"Dev","version":"0.0.1","enabled":true,"manifest_path":"/src/dev/herdr-plugin.toml","plugin_root":"/src/dev",` +
	`"source":{"kind":"local"}}]}}`

func TestExportThenRestoreElsewhere(t *testing.T) {
	from := newHarness(t)
	from.setInstalled(gadgetAndLink)
	file := filepath.Join(t.TempDir(), "plugins.json")
	_, stderr, err := from.run("", false, "export", "-o", file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "Exported 1 plugin to "+file) || !strings.Contains(stderr, "Skipped, linked locally: me.dev") {
		t.Errorf("export summary:\n%s", stderr)
	}

	to := newHarness(t)
	to.installs(`{"result":{"type":"plugin_list","plugins":[{"plugin_id":"carol.gadget","name":"Gadget","version":"0.2.0","enabled":true,"manifest_path":"/x","plugin_root":"/x",` +
		`"source":{"kind":"github","owner":"carol","repo":"gadget","requested_ref":"` + head + `","resolved_commit":"` + head + `"}}]}}`)
	out, _, err := to.run("", false, "import", file)
	if !errors.Is(err, errNeedYes) {
		t.Fatalf("without a terminal or --yes: %v", err)
	}
	for _, want := range []string{
		"carol.gadget  install 0.2.0 at default branch (bbbbbbbbbbbb), enabled; follows the default branch",
		"1 to change.",
		"== carol.gadget (install)", "Build commands",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan lacks %q:\n%s", want, out)
		}
	}
	out, _, err = to.run("", false, "import", file, "--dry-run")
	if err != nil || strings.Contains(out, "me.dev") || !strings.Contains(out, "Dry run: nothing was changed.") {
		t.Errorf("dry run: %v\n%s", err, out)
	}
	if to.ran("plugin install") {
		t.Fatalf("installed before the plan was confirmed: %q", to.calls())
	}

	out, _, err = to.run("", false, "import", file, "--yes")
	if err != nil {
		t.Errorf("err = %v", err)
	}
	if !to.ran("plugin install carol/gadget --ref " + head + " --yes") {
		t.Errorf("herdr was not asked for the exported commit: %q", to.calls())
	}
	if !strings.Contains(out, "carol.gadget  imported  carol.gadget is installed: 0.2.0 at default branch (bbbbbbbbbbbb), enabled") {
		t.Errorf("the results do not say how each plugin ended:\n%s", out)
	}
	if out, _, _ := to.run("", false, "info", "carol.gadget"); !strings.Contains(out, "updates: follows the default branch") {
		t.Errorf("the restored plugin does not follow the exported ref:\n%s", out)
	}
	if out, _, _ := to.run("", false, "history"); !strings.Contains(out, "import  carol.gadget  done") {
		t.Errorf("the restore is not in the history:\n%s", out)
	}

	out, _, err = to.run("", false, "import", file, "--yes")
	if err != nil || !strings.Contains(out, "unchanged: already installed as exported") || !strings.Contains(out, "Nothing to import.") {
		t.Errorf("restoring again: %v\n%s", err, out)
	}

	// With only a local link, stdout still carries an export import reads.
	only := newHarness(t)
	only.setInstalled(`{"result":{"type":"plugin_list","plugins":[` +
		`{"plugin_id":"me.dev","name":"Dev","version":"0.0.1","enabled":true,"manifest_path":"/src/dev/herdr-plugin.toml","plugin_root":"/src/dev",` +
		`"source":{"kind":"local"}}]}}`)
	stdout, stderr, err := only.run("", false, "export")
	if err != nil || !strings.Contains(stdout, `"plugins": []`) || !strings.Contains(stderr, "Exported 0 plugins to stdout") {
		t.Fatalf("export of a local link only: %v\n%s\n%s", err, stdout, stderr)
	}
	empty := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(empty, []byte(stdout), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, _, err := to.run("", false, "import", empty, "--yes"); err != nil || !strings.Contains(out, "Nothing to import.") {
		t.Errorf("import of the empty export: %v\n%s", err, out)
	}
}

// failInstallsOf makes herdr fail to install any source under owner, and
// run as before otherwise.
func failInstallsOf(t *testing.T, h *harness, owner string) {
	t.Helper()
	bin := h.m.CLI.Bin
	if err := os.Rename(bin, bin+".orig"); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncase \"$*\" in\n'plugin install " + owner + "/'*) echo \"$*\" >> " + filepath.Join(filepath.Dir(bin), "calls.txt") +
		"; echo 'build failed'; exit 1 ;;\nesac\nexec " + bin + ".orig \"$@\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeExport(t *testing.T, plugins string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "plugins.json")
	data := `{"format":"hpm-plugins","version":1,"exported_at":"2026-09-29T12:00:00Z","plugins":[` + plugins + `]}`
	if err := os.WriteFile(file, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestRestoreReportsEachPlugin(t *testing.T) {
	h := newHarness(t)
	h.manifestIDs["dave/tool"] = "dave.tool"
	h.manifestIDs["erin/quiet"] = "erin.quiet"
	h.installs(gadgetAt(head))
	failInstallsOf(t, h, "dave")
	file := writeExport(t,
		`{"id":"dave.tool","enabled":true,"kind":"github","source":"dave/tool","commit":"`+head+`"},`+
			`{"id":"carol.gadget","enabled":true,"kind":"github","source":"carol/gadget","ref":"main","commit":"`+head+`"},`+
			`{"id":"erin.quiet","enabled":false,"kind":"github","source":"erin/quiet","commit":"`+head+`"}`)
	out, stderr, err := h.run("", false, "import", file, "--yes")
	if err == nil || err.Error() != "failed: dave.tool\nnot imported: erin.quiet" {
		t.Errorf("err = %v", err)
	}
	// No server is running, so erin.quiet could not be kept disabled.
	if !strings.Contains(out, "erin.quiet    install") || !strings.Contains(out, "cannot import: keeping the plugin disabled needs a running herdr server") {
		t.Errorf("the plan does not say why erin.quiet cannot be restored:\n%s", out)
	}
	if !strings.Contains(stderr, "build failed") {
		t.Errorf("herdr's failure is not shown:\n%s", stderr)
	}
	for _, want := range []string{"dave.tool     failed", "carol.gadget  imported", "erin.quiet    not imported"} {
		if !strings.Contains(out, want) {
			t.Errorf("the results lack %q:\n%s", want, out)
		}
	}
	if !h.ran("plugin install carol/gadget --ref "+head+" --yes") || h.ran("plugin install erin/quiet") {
		t.Errorf("calls = %q", h.calls())
	}
}

func TestRestoreDeclinedChangesNothing(t *testing.T) {
	h := newHarness(t)
	file := writeExport(t, `{"id":"carol.gadget","enabled":true,"kind":"github","source":"carol/gadget","commit":"`+head+`"}`)
	out, _, err := h.run("n\n", true, "import", file)
	if !errors.Is(err, errCancelled) || !strings.Contains(out, "Nothing was changed.") {
		t.Errorf("declined: %v\n%s", err, out)
	}
	if h.ran("plugin install") {
		t.Errorf("installed after the plan was declined: %q", h.calls())
	}
}

func TestRestoreRefusesABadFileOrId(t *testing.T) {
	h := newHarness(t)
	file := writeExport(t, `{"id":"carol.gadget","enabled":true,"kind":"github","source":"carol/gadget","commit":"`+head+`"}`)
	if _, _, err := h.run("", false, "import", file, "o.nope"); err == nil || !strings.Contains(err.Error(), "does not list o.nope") {
		t.Errorf("an id not in the export: %v", err)
	}
	if _, _, err := h.run("", false, "import", file, "--exclude", "o.nope"); err == nil || !strings.Contains(err.Error(), "does not list o.nope") {
		t.Errorf("an exclude not in the export: %v", err)
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte(`{"format":"hpm-plugins","version":9}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.run("", false, "import", bad); err == nil || !strings.Contains(err.Error(), "from a newer hpm") {
		t.Errorf("a newer file: %v", err)
	}
}

func TestRestoreIDs(t *testing.T) {
	exp := &manager.Export{Plugins: []manager.ExportedPlugin{{ID: "a"}, {ID: "b"}, {ID: "c"}}}
	for _, tt := range []struct {
		named, exclude, want []string
	}{
		{nil, nil, nil},
		{nil, []string{"b"}, []string{"a", "c"}},
		{[]string{"c", "a", "c"}, []string{"a"}, []string{"c"}},
		{nil, []string{"a", "b", "c"}, []string{}},
	} {
		got, err := restoreIDs(exp, tt.named, tt.exclude)
		if err != nil || !slices.Equal(got, tt.want) || (got == nil) != (tt.want == nil) {
			t.Errorf("named %v, exclude %v: %v, %v; want %v", tt.named, tt.exclude, got, err, tt.want)
		}
	}
}

func TestRestoreStopsStartingOnceCancelled(t *testing.T) {
	h := newHarness(t)
	items := []manager.RestoreItem{
		{Plugin: manager.ExportedPlugin{ID: "a"}, Action: manager.RestoreInstall},
		{Plugin: manager.ExportedPlugin{ID: "b"}, Action: manager.RestoreEnable},
		{Plugin: manager.ExportedPlugin{ID: "c"}, Action: manager.RestoreUnchanged},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &cli{m: h.m, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}}
	results := c.runRestore(ctx, items)
	if err := restoreError(items, results); err == nil || err.Error() != "not started: a, b" {
		t.Errorf("err = %v", err)
	}
	if h.ran("plugin install") {
		t.Errorf("started after the cancellation: %q", h.calls())
	}
}

// changeFirst runs change when the confirmation is read, as another
// process could between the plan and its confirmation, then answers yes.
type changeFirst struct {
	change func()
	done   bool
}

func (r *changeFirst) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.change()
	r.done = true
	return copy(p, "y\n"), nil
}

func TestRestoreDoesNotRunOverAChangeMadeAfterThePlan(t *testing.T) {
	h := newHarness(t)
	h.installs(gadgetAt(head))
	file := writeExport(t, `{"id":"carol.gadget","enabled":true,"kind":"github","source":"carol/gadget","commit":"`+head+`"}`)
	var out, errOut bytes.Buffer
	in := &changeFirst{change: func() {
		h.setInstalled(strings.Replace(gadgetAt(""), `"owner":"carol","repo":"gadget"`, `"owner":"dave","repo":"gadget"`, 1))
	}}
	c := &cli{m: h.m, in: in, out: &out, errOut: &errOut, interactive: true}
	root := c.root(nil)
	root.SetArgs([]string{"import", file})
	err := root.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "changed since planned: carol.gadget") || !strings.Contains(err.Error(), "review the plan") {
		t.Errorf("err = %v", err)
	}
	if !strings.Contains(out.String(), "carol.gadget  changed since planned") || !strings.Contains(errOut.String(), "carol.gadget was not installed when planned, and is dave/gadget 0.2.0 at default branch") {
		t.Errorf("the result does not say the plugin changed:\n%s\n%s", out.String(), errOut.String())
	}
	if h.ran("plugin install") {
		t.Errorf("installed over the plugin installed since the plan: %q", h.calls())
	}
}
