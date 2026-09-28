package herdrcli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vika2603/herdr-client/herdr"
)

// fakeHerdr writes a shell script standing in for herdr. It records its
// arguments in args.txt next to itself and then runs body.
func fakeHerdr(t *testing.T, body string) (r Runner, argsFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the herdr binary")
	}
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args.txt")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\n" + body + "\n"
	bin := filepath.Join(dir, "herdr")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return Runner{Bin: bin}, argsFile
}

func readArgs(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(string(data)), " ")
}

func TestInstallArguments(t *testing.T) {
	r, argsFile := fakeHerdr(t, `echo "Installed o.r from o/r."`)
	var out bytes.Buffer
	if err := r.Install(context.Background(), "o/r/sub", "v1.0.0", &out); err != nil {
		t.Fatal(err)
	}
	if got := readArgs(t, argsFile); got != "plugin install o/r/sub --ref v1.0.0 --yes" {
		t.Errorf("args = %q", got)
	}
	if !strings.Contains(out.String(), "Installed o.r") {
		t.Errorf("output not forwarded: %q", out.String())
	}

	if err := r.Install(context.Background(), "o/r", "", nil); err != nil {
		t.Fatal(err)
	}
	if got := readArgs(t, argsFile); got != "plugin install o/r --yes" {
		t.Errorf("args without ref = %q", got)
	}
}

func TestFailureCarriesOutput(t *testing.T) {
	r, _ := fakeHerdr(t, `echo "plugin not installed: x" >&2; exit 1`)
	err := r.Uninstall(context.Background(), "x", nil)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 || exitErr.Output != "plugin not installed: x" {
		t.Fatalf("err = %#v", err)
	}
}

func TestList(t *testing.T) {
	r, argsFile := fakeHerdr(t, `echo '{"id":"cli:plugin","result":{"type":"plugin_list","plugins":[{"plugin_id":"o.r","name":"R","version":"1.0.0","enabled":false,"manifest_path":"/x/herdr-plugin.toml","plugin_root":"/x"}]}}'`)
	plugins, err := r.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 1 || plugins[0].PluginID != "o.r" || plugins[0].Enabled {
		t.Errorf("plugins = %+v", plugins)
	}
	if got := readArgs(t, argsFile); got != "plugin list --json" {
		t.Errorf("args = %q", got)
	}
}

func TestListError(t *testing.T) {
	r, _ := fakeHerdr(t, `echo '{"id":"cli:plugin","error":{"code":"plugin_registry_load_failed","message":"bad registry"}}'`)
	_, err := r.List(context.Background())
	if !herdr.IsCode(err, "plugin_registry_load_failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestVersion(t *testing.T) {
	r, _ := fakeHerdr(t, `echo "herdr 0.9.1"`)
	v, err := r.Version(context.Background())
	if err != nil || v != "0.9.1" {
		t.Fatalf("Version = %q, %v", v, err)
	}
}

func TestCommandEnvDropsPluginContext(t *testing.T) {
	r, _ := fakeHerdr(t, `env > "$(dirname "$0")/env.txt"`)
	t.Setenv("HERDR_PLUGIN_ID", "manager")
	t.Setenv("HERDR_PLUGIN_STATE_DIR", "/state")
	t.Setenv("HERDR_SOCKET_PATH", "/tmp/herdr.sock")
	if err := r.Install(context.Background(), "o/r", "", nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(r.Bin), "env.txt"))
	if err != nil {
		t.Fatal(err)
	}
	env := string(data)
	if strings.Contains(env, "HERDR_PLUGIN_") {
		t.Errorf("plugin variables reached the command:\n%s", env)
	}
	if !strings.Contains(env, "HERDR_SOCKET_PATH=/tmp/herdr.sock") {
		t.Error("socket path was not passed on")
	}
}
