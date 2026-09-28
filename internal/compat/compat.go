// Package compat checks whether a plugin can run on this machine: its
// declared platforms and the minimum herdr version it asks for.
package compat

import (
	"fmt"
	"runtime"
	"slices"
	"strings"

	"golang.org/x/mod/semver"
)

// Platform is the manifest platform name of the running OS: "linux",
// "macos" or "windows". Other systems keep their GOOS name, which no
// manifest lists.
func Platform() string {
	if runtime.GOOS == "darwin" {
		return "macos"
	}
	return runtime.GOOS
}

// Problems lists the reasons a plugin with the given platforms and
// min_herdr_version cannot run under herdrVersion on platform. An undeclared
// platform list is treated as supporting every platform, and an unknown
// herdrVersion skips the version check.
func Problems(platforms []string, minHerdrVersion, herdrVersion, platform string) []string {
	var out []string
	if len(platforms) > 0 && !slices.Contains(platforms, platform) {
		out = append(out, fmt.Sprintf("supports %s, not %s", strings.Join(platforms, ", "), platform))
	}
	if minHerdrVersion != "" && herdrVersion != "" {
		want, cur := canonical(minHerdrVersion), canonical(herdrVersion)
		if semver.IsValid(want) && semver.IsValid(cur) && semver.Compare(cur, want) < 0 {
			out = append(out, fmt.Sprintf("requires herdr %s, running %s", minHerdrVersion, herdrVersion))
		}
	}
	return out
}

func canonical(v string) string {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}
