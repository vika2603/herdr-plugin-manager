package manager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vika2603/herdr-client/herdr"
)

func TestHerdrConfigPathPlatformDefaults(t *testing.T) {
	homePath, xdg, roaming, profile := "/home/test", "/xdg", "/roaming", "/profile"
	home := func() (string, error) {
		if homePath == "" {
			return "", errors.New("no home")
		}
		return homePath, nil
	}
	tests := []struct {
		name, goos string
		env        map[string]string
		want       string
	}{
		{"explicit path", "windows", map[string]string{"HERDR_CONFIG_PATH": "/override.toml", "XDG_CONFIG_HOME": "/xdg"}, "/override.toml"},
		{"XDG override", "windows", map[string]string{"XDG_CONFIG_HOME": xdg, "APPDATA": roaming}, filepath.Join(xdg, "herdr", "config.toml")},
		{"Windows roaming data", "windows", map[string]string{"APPDATA": roaming, "USERPROFILE": profile}, filepath.Join(roaming, "herdr", "config.toml")},
		{"Windows profile", "windows", map[string]string{"USERPROFILE": profile}, filepath.Join(profile, "AppData", "Roaming", "herdr", "config.toml")},
		{"Unix home", "linux", map[string]string{"APPDATA": roaming}, filepath.Join(homePath, ".config", "herdr", "config.toml")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := herdrConfigPath(tt.goos, func(k string) string { return tt.env[k] }, home)
			if got != tt.want {
				t.Fatalf("herdrConfigPath() = %q, want %q", got, tt.want)
			}
		})
	}
	homePath = ""
	if got := herdrConfigPath("windows", func(string) string { return "" }, home); got != filepath.Join("~", ".config", "herdr", "config.toml") {
		t.Fatalf("no home: got %q", got)
	}
}

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
