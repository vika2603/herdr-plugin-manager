package main

import (
	"os"
	"regexp"
	"testing"

	"github.com/vika2603/herdr-client/plugin/plugintest"

	"github.com/vika2603/herdr-plugin-manager/internal/app"
)

func TestManifest(t *testing.T) {
	plugintest.CheckManifest(t, "../../herdr-plugin.toml", newPlugin())
}

func TestManifestMatchesBinary(t *testing.T) {
	data, err := os.ReadFile("../../herdr-plugin.toml")
	if err != nil {
		t.Fatal(err)
	}
	for field, want := range map[string]string{"version": app.Version, "id": app.PluginID} {
		m := regexp.MustCompile(`(?m)^` + field + ` = "([^"]+)"`).FindSubmatch(data)
		if len(m) < 2 || string(m[1]) != want {
			t.Errorf("manifest %s %q, binary %q", field, m, want)
		}
	}
}
