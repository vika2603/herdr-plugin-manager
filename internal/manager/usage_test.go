package manager

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vika2603/herdr-client/herdr"
)

func withActions(ids ...string) herdr.InstalledPluginInfo {
	p := at("v1.0.0", commitV1, true)
	var actions []herdr.PluginManifestAction
	for _, id := range ids {
		actions = append(actions, herdr.PluginManifestAction{ID: id, Title: "Do " + id})
	}
	p.Actions = herdr.Some(actions)
	return p
}

func TestUsageFindsTheKeysBoundToEachAction(t *testing.T) {
	config := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(config, []byte(`
[keys]
split = "prefix+s"

[[keys.command]]
key = "prefix+o"
type = "plugin_action"
command = "o.r.open"

[[keys.command]]
key = "prefix+O"
type = "plugin_action"
command = "open"

[[keys.command]]
key = "prefix+x"
type = "shell"
command = "o.r.close"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_CONFIG_PATH", config)
	cli, _ := fakeHerdr(t, `echo /cfg/o.r`)
	m := &Manager{CLI: cli}
	u := m.Usage(context.Background(), withActions("open", "close"))
	if u.ConfigDir != "/cfg/o.r" || u.ConfigErr != nil || u.KeysErr != nil || u.HerdrConfig != config {
		t.Fatalf("usage = %+v", u)
	}
	if len(u.Actions) != 2 || !slices.Equal(u.Actions[0].Keys, []string{"prefix+o", "prefix+O"}) || len(u.Actions[1].Keys) != 0 {
		t.Fatalf("actions = %+v; want open bound twice and close, bound only as a shell command, not at all", u.Actions)
	}
	var text []string
	for _, s := range u.Sections() {
		text = append(text, s.Title+" "+s.Note)
		text = append(text, s.Lines...)
	}
	out := strings.Join(text, "\n")
	for _, want := range []string{
		"config directory: /cfg/o.r",
		"action open: Do open, run as o.r.open; bound to prefix+o, prefix+O",
		"action close: Do close, run as o.r.close",
		`command = "o.r.close"`, `type = "plugin_action"`,
		"in " + config + ", then herdr server reload-config",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("usage does not say %q:\n%s", want, out)
		}
	}
}

func TestUsageSaysWhenTheBindingsCannotBeRead(t *testing.T) {
	config := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(config, []byte("[keys\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_CONFIG_PATH", config)
	cli, _ := fakeHerdr(t, `echo /cfg/o.r`)
	u := (&Manager{CLI: cli}).Usage(context.Background(), withActions("open"))
	if u.KeysErr == nil {
		t.Fatal("a config that does not parse gave no error")
	}
	if len(u.Actions) != 1 || len(u.Actions[0].Keys) != 0 {
		t.Errorf("actions = %+v", u.Actions)
	}
}

func TestUsageWithoutAConfigFileBindsNothing(t *testing.T) {
	t.Setenv("HERDR_CONFIG_PATH", filepath.Join(t.TempDir(), "missing.toml"))
	cli, _ := fakeHerdr(t, `echo /cfg/o.r`)
	u := (&Manager{CLI: cli}).Usage(context.Background(), withActions("open"))
	if u.KeysErr != nil || len(u.Actions) != 1 || len(u.Actions[0].Keys) != 0 {
		t.Errorf("usage = %+v", u)
	}
}

func TestHerdrConfigPath(t *testing.T) {
	t.Setenv("HERDR_CONFIG_PATH", "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got := HerdrConfigPath(); got != "/xdg/herdr/config.toml" {
		t.Errorf("with XDG_CONFIG_HOME: %s", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/u")
	if got := HerdrConfigPath(); got != "/home/u/.config/herdr/config.toml" {
		t.Errorf("with HOME only: %s", got)
	}
	t.Setenv("HERDR_CONFIG_PATH", "/etc/h.toml")
	if got := HerdrConfigPath(); got != "/etc/h.toml" {
		t.Errorf("with HERDR_CONFIG_PATH: %s", got)
	}
}
