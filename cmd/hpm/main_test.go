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

func TestManifestVersionMatchesBinary(t *testing.T) {
	data, err := os.ReadFile("../../herdr-plugin.toml")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^version = "([^"]+)"`).FindSubmatch(data)
	if len(m) < 2 || string(m[1]) != app.Version {
		t.Fatalf("manifest version %q, app.Version %q", m, app.Version)
	}
}
