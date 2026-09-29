package manager

import (
	"strings"
	"testing"

	"github.com/vika2603/herdr-client/herdr"
)

func TestTracking(t *testing.T) {
	local := herdr.InstalledPluginInfo{PluginID: "l", PluginRoot: "/src/l"}
	for _, tt := range []struct {
		p    herdr.InstalledPluginInfo
		want string
	}{
		{at("v1.0.0", commitV1, true), "follows new releases, installed at v1.0.0"},
		{at("", commitV1, true), "follows the default branch"},
		{at("dev", commitV1, true), "follows dev when it moves"},
		{at(commitV1, commitV1, true), "pinned to commit 111111111111; no updates until it is unpinned"},
		{local, "linked locally; update its working tree, this manager does not"},
	} {
		if got := TrackingOf(tt.p).Describe(); got != tt.want {
			t.Errorf("%s: %q, want %q", StateOf(tt.p), got, tt.want)
		}
	}
}

func TestVersionRef(t *testing.T) {
	release, pinned := at("v1.0.0", commitV1, true), at(commitV1, commitV1, true)
	for _, tt := range []struct {
		name    string
		p       herdr.InstalledPluginInfo
		kind    ChangeKind
		ref     string
		want    string
		wantErr string
	}{
		{"switch", release, KindSwitch, "v0.9.0", "v0.9.0", ""},
		{"switch needs a ref", release, KindSwitch, "", "", "name the version"},
		{"pin holds the commit", release, KindPin, "", commitV1, ""},
		{"pin twice", pinned, KindPin, "", "", "already pinned"},
		{"unpin to what install picks", pinned, KindUnpin, "", "", ""},
		{"unpin to a branch", pinned, KindUnpin, "main", "main", ""},
		{"unpin what is not pinned", release, KindUnpin, "", "", "is not pinned"},
		{"reinstall the ref", release, KindReinstall, "", "v1.0.0", ""},
		{"a linked plugin", herdr.InstalledPluginInfo{PluginID: "l"}, KindSwitch, "v1", "", "linked locally"},
	} {
		got, err := VersionRef(tt.p, tt.kind, tt.ref)
		switch {
		case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
			t.Errorf("%s: err = %v, want %q", tt.name, err, tt.wantErr)
		case tt.wantErr == "" && (err != nil || got != tt.want):
			t.Errorf("%s: %q, %v; want %q", tt.name, got, err, tt.want)
		}
	}
}

func TestRequireInstalledCommit(t *testing.T) {
	p := &Preview{Commit: commitV2}
	p.RequireInstalledCommit(at("", commitV1, true))
	if len(p.Problems) != 1 || !strings.Contains(p.Problems[0], "reinstalling it would update the plugin") {
		t.Errorf("problems = %q", p.Problems)
	}
}
