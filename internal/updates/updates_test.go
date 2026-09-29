package updates

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vika2603/herdr-client/herdr"
)

const lsRemote = "ref: refs/heads/main\tHEAD\n" +
	"1111111111111111111111111111111111111111\tHEAD\n" +
	"1111111111111111111111111111111111111111\trefs/heads/main\n" +
	"2222222222222222222222222222222222222222\trefs/heads/dev\n" +
	"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\trefs/tags/v0.1.0\n" +
	"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\trefs/tags/v0.2.0\n" +
	"cccccccccccccccccccccccccccccccccccccccc\trefs/tags/v0.2.0^{}\n" +
	"dddddddddddddddddddddddddddddddddddddddd\trefs/tags/v0.3.0-rc.1\n" +
	"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee\trefs/tags/nightly\n"

func TestParseLsRemote(t *testing.T) {
	refs := ParseLsRemote(lsRemote)
	if refs.Head != strings.Repeat("1", 40) {
		t.Errorf("head = %q", refs.Head)
	}
	if refs.Branches["dev"] != strings.Repeat("2", 40) {
		t.Errorf("dev = %q", refs.Branches["dev"])
	}
	if got := refs.Tags["v0.2.0"]; got != strings.Repeat("c", 40) {
		t.Errorf("annotated tag should resolve to the peeled commit, got %q", got)
	}
	if _, ok := refs.Tags["v0.2.0^{}"]; ok {
		t.Error("peeled entry kept as its own tag")
	}
	if refs.HeadBranch != "main" {
		t.Errorf("head branch = %q", refs.HeadBranch)
	}
	if got := strings.Join(refs.Releases(), ","); got != "v0.3.0-rc.1,v0.2.0,v0.1.0" {
		t.Errorf("releases = %s, want newest first without non-release tags", got)
	}
	if got := refs.LatestRelease(); got != "v0.2.0" {
		t.Errorf("latest release = %q, want the newest that is not a pre-release", got)
	}
}

type fakeLister struct {
	refs  Refs
	err   error
	calls int
}

func (f *fakeLister) List(context.Context, string) (Refs, error) {
	f.calls++
	return f.refs, f.err
}

func github(ref, commit string) herdr.InstalledPluginInfo {
	src := herdr.PluginSourceInfo{
		Kind:           herdr.Some(herdr.PluginSourceKindGithub),
		Owner:          herdr.Some("o"),
		Repo:           herdr.Some("r"),
		ResolvedCommit: herdr.Some(commit),
	}
	if ref != "" {
		src.RequestedRef = herdr.Some(ref)
	}
	return herdr.InstalledPluginInfo{PluginID: "o.r", Source: herdr.Some(src)}
}

func TestCheck(t *testing.T) {
	c := strings.Repeat
	tests := []struct {
		name       string
		info       herdr.InstalledPluginInfo
		wantKind   Kind
		wantTarget string
	}{
		{"older release tag", github("v0.1.0", c("a", 40)), Available, "v0.2.0"},
		{"latest release tag", github("v0.2.0", c("c", 40)), UpToDate, ""},
		{"prerelease sees prereleases", github("v0.3.0-rc.1", c("d", 40)), UpToDate, ""},
		{"default branch moved", github("", c("9", 40)), Available, ""},
		{"default branch current", github("", c("1", 40)), UpToDate, ""},
		{"branch moved", github("dev", c("1", 40)), Available, "dev"},
		{"non-release tag unchanged", github("nightly", c("e", 40)), UpToDate, "nightly"},
		{"commit pin", github(c("f", 40), c("f", 40)), Pinned, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lister := &fakeLister{refs: ParseLsRemote(lsRemote)}
			got, err := Check(context.Background(), lister, tt.info)
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind != tt.wantKind {
				t.Fatalf("kind = %s, want %s (%+v)", got.Kind, tt.wantKind, got)
			}
			if got.Kind == Available && got.TargetRef != tt.wantTarget {
				t.Errorf("target ref = %q, want %q", got.TargetRef, tt.wantTarget)
			}
			if tt.wantKind == Pinned && lister.calls != 0 {
				t.Error("a pinned plugin should not query the remote")
			}
		})
	}
}

func TestShortTagsAreNotReleases(t *testing.T) {
	refs := ParseLsRemote(lsRemote + "1111111111111111111111111111111111111111\trefs/tags/v1\n" +
		"3333333333333333333333333333333333333333\trefs/tags/v2\n" +
		"4444444444444444444444444444444444444444\trefs/tags/v0.2\n")
	got, err := Check(context.Background(), &fakeLister{refs: refs}, github("v0.1.0", strings.Repeat("a", 40)))
	if err != nil || got.TargetRef != "v0.2.0" {
		t.Fatalf("got %+v, %v; want the v0.2.0 release, not a shorthand tag", got, err)
	}
}

func TestCheckPrereleaseUpgradesToNewerStable(t *testing.T) {
	refs := ParseLsRemote(lsRemote + "ffffffffffffffffffffffffffffffffffffffff\trefs/tags/v0.3.0\n")
	got, err := Check(context.Background(), &fakeLister{refs: refs}, github("v0.3.0-rc.1", strings.Repeat("d", 40)))
	if err != nil || got.Kind != Available || got.TargetRef != "v0.3.0" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestCheckLocalAndMissingRef(t *testing.T) {
	local := herdr.InstalledPluginInfo{PluginID: "x", Source: herdr.Some(herdr.PluginSourceInfo{Kind: herdr.Some(herdr.PluginSourceKindLocal)})}
	lister := &fakeLister{refs: ParseLsRemote(lsRemote)}
	if got, err := Check(context.Background(), lister, local); err != nil || got.Kind != Local || lister.calls != 0 {
		t.Fatalf("local: %+v, %v, calls=%d", got, err, lister.calls)
	}
	if _, err := Check(context.Background(), lister, github("gone", "x")); err == nil {
		t.Fatal("a ref missing upstream should be an error")
	}
	failing := &fakeLister{err: errors.New("network down")}
	if _, err := Check(context.Background(), failing, github("", "x")); err == nil {
		t.Fatal("lister error not returned")
	}
}

func TestDescribe(t *testing.T) {
	r := Result{Kind: Available, CurrentRef: "v0.1.0", TargetRef: "v0.2.0"}
	if got := r.Describe(); got != "v0.1.0 -> v0.2.0" {
		t.Errorf("tag: %q", got)
	}
	r = Result{Kind: Available, CurrentCommit: strings.Repeat("1", 40), TargetCommit: strings.Repeat("2", 40)}
	if got := r.Describe(); got != "new commits on the default branch (111111111111 -> 222222222222)" {
		t.Errorf("branch: %q", got)
	}
}
