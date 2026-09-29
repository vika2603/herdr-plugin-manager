package manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vika2603/herdr-client/herdr"
)

func TestCheckBindingsFindsKeysThatRunNothing(t *testing.T) {
	config := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(config, []byte(`
[[keys.command]]
key = "prefix+a"
type = "plugin_action"
command = "o.r.open"
[[keys.command]]
key = "prefix+b"
type = "plugin_action"
command = "gone.action"
[[keys.command]]
key = "prefix+c"
type = "plugin_action"
command = "stop"
[[keys.command]]
key = "prefix+d"
type = "plugin_action"
command = "o.s.run"
[[keys.command]]
key = "prefix+e"
type = "shell"
command = "anything"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := withActions("open", "stop")
	s := withActions("stop", "run")
	s.PluginID, s.Enabled = "o.s", false
	f := checkBindings(config, []herdr.InstalledPluginInfo{r, s})
	if f.Health != Warning || f.Summary != "3 of 4 keys bound to plugin actions have problems" {
		t.Fatalf("finding = %+v", f)
	}
	want := []string{
		"prefix+b runs gone.action, which no installed plugin declares",
		"prefix+c runs stop, which more than one plugin declares; name it as <plugin id>.<action id>",
		"prefix+d runs o.s.run, but o.s is disabled",
	}
	if strings.Join(f.Details, "\n") != strings.Join(want, "\n") {
		t.Errorf("details:\n%s\nwant:\n%s", strings.Join(f.Details, "\n"), strings.Join(want, "\n"))
	}
	if f := checkBindings(filepath.Join(t.TempDir(), "none.toml"), nil); f.Health != Healthy {
		t.Errorf("no config: %+v", f)
	}
}

func TestCheckPluginsReportsWhatCannotRunHere(t *testing.T) {
	ok := withActions()
	ok.PluginRoot = t.TempDir()
	other := withActions()
	other.PluginID, other.PluginRoot = "o.other", t.TempDir()
	other.Platforms = herdr.Some([]herdr.PluginPlatform{"windows"})
	other.MinHerdrVersion = herdr.Some("9.0.0")
	gone := withActions()
	gone.PluginID, gone.PluginRoot = "o.gone", filepath.Join(t.TempDir(), "missing")
	warned := withActions()
	warned.PluginID, warned.PluginRoot = "o.warned", t.TempDir()
	warned.Warnings = herdr.Some([]string{"manifest does not declare platforms"})
	m := &Manager{Platform: "linux"}
	out := m.checkPlugins([]herdr.InstalledPluginInfo{ok, other, gone, warned}, "0.9.1")
	if len(out) != 4 || out[0].Area != "plugins" || out[0].Health != Failing || out[0].Summary != "4 installed, 3 with problems" {
		t.Fatalf("findings = %+v", out)
	}
	byArea := map[string]Finding{}
	for _, f := range out[1:] {
		byArea[f.Area] = f
	}
	if f := byArea["plugin o.other"]; f.Health != Failing || !strings.Contains(strings.Join(f.Details, ";"), "supports windows, not linux") ||
		!strings.Contains(strings.Join(f.Details, ";"), "requires herdr 9.0.0, running 0.9.1") {
		t.Errorf("o.other = %+v", f)
	}
	if f := byArea["plugin o.gone"]; f.Health != Failing || !strings.Contains(f.Details[0], "is missing") {
		t.Errorf("o.gone = %+v", f)
	}
	if f := byArea["plugin o.warned"]; f.Health != Warning || f.Details[0] != "herdr warns: manifest does not declare platforms" {
		t.Errorf("o.warned = %+v", f)
	}
}
