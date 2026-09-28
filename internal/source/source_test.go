package source

import (
	"testing"

	"github.com/vika2603/herdr-client/herdr"
)

func TestParse(t *testing.T) {
	tests := []struct {
		in      string
		want    GitHub
		wantErr bool
	}{
		{in: "owner/repo", want: GitHub{Owner: "owner", Repo: "repo"}},
		{in: "owner/repo/plugins/foo", want: GitHub{Owner: "owner", Repo: "repo", Subdir: "plugins/foo"}},
		{in: " owner/repo/ ", want: GitHub{Owner: "owner", Repo: "repo"}},
		{in: "owner", wantErr: true},
		{in: "https://github.com/owner/repo", wantErr: true},
		{in: "git@github.com:owner/repo", wantErr: true},
		{in: "owner//repo", wantErr: true},
		{in: "owner/repo/../x", wantErr: true},
	}
	for _, tt := range tests {
		got, err := Parse(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("Parse(%q) = %+v, want error", tt.in, got)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", tt.in, got, err, tt.want)
		}
	}
}

func TestFromManifestPath(t *testing.T) {
	if got := FromManifestPath("o", "r", "herdr-plugin.toml"); got.String() != "o/r" {
		t.Errorf("root manifest: got %q", got)
	}
	if got := FromManifestPath("o", "r", "a/b/herdr-plugin.toml"); got.String() != "o/r/a/b" {
		t.Errorf("nested manifest: got %q", got)
	}
}

func TestFromInstalled(t *testing.T) {
	github := herdr.InstalledPluginInfo{Source: herdr.Some(herdr.PluginSourceInfo{
		Kind:   herdr.Some(herdr.PluginSourceKindGithub),
		Owner:  herdr.Some("o"),
		Repo:   herdr.Some("r"),
		Subdir: herdr.Some("sub"),
	})}
	if got, ok := FromInstalled(github); !ok || got.String() != "o/r/sub" {
		t.Errorf("github source: got %q, %v", got, ok)
	}
	local := herdr.InstalledPluginInfo{Source: herdr.Some(herdr.PluginSourceInfo{Kind: herdr.Some(herdr.PluginSourceKindLocal)})}
	if _, ok := FromInstalled(local); ok {
		t.Error("local source reported as GitHub")
	}
}

func TestManifestURL(t *testing.T) {
	g := GitHub{Owner: "o", Repo: "r", Subdir: "p/q"}
	want := "https://raw.githubusercontent.com/o/r/abc/p/q/herdr-plugin.toml"
	if got := g.ManifestURL("abc"); got != want {
		t.Errorf("ManifestURL = %q, want %q", got, want)
	}
	if got := (GitHub{Owner: "o", Repo: "r"}).ManifestURL(""); got != "https://raw.githubusercontent.com/o/r/HEAD/herdr-plugin.toml" {
		t.Errorf("ManifestURL default ref = %q", got)
	}
}
