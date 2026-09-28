// Package source handles the owner/repo[/subdir...] shorthand that
// `herdr plugin install` accepts for GitHub plugins.
package source

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/vika2603/herdr-client/herdr"
)

// ManifestFile is the manifest name herdr looks for in a plugin directory.
const ManifestFile = "herdr-plugin.toml"

// GitHub is a plugin directory inside a GitHub repository. Subdir is empty for
// a manifest at the repository root.
type GitHub struct {
	Owner  string
	Repo   string
	Subdir string
}

// Parse reads the shorthand. It rejects URLs and malformed segments early;
// herdr itself remains the authority on what it accepts.
func Parse(value string) (GitHub, error) {
	value = strings.TrimSpace(value)
	if strings.Contains(value, ":") || strings.HasPrefix(value, "git@") {
		return GitHub{}, errors.New("expected owner/repo[/subdir] shorthand, not a URL")
	}
	parts := strings.Split(strings.Trim(value, "/"), "/")
	if len(parts) < 2 {
		return GitHub{}, fmt.Errorf("expected owner/repo[/subdir], got %q", value)
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return GitHub{}, fmt.Errorf("invalid path segment in %q", value)
		}
	}
	return GitHub{Owner: parts[0], Repo: parts[1], Subdir: strings.Join(parts[2:], "/")}, nil
}

// FromManifestPath builds the source for a manifest found at manifestPath
// inside owner/repo, such as "plugins/foo/herdr-plugin.toml".
func FromManifestPath(owner, repo, manifestPath string) GitHub {
	dir := path.Dir(strings.Trim(manifestPath, "/"))
	if dir == "." {
		dir = ""
	}
	return GitHub{Owner: owner, Repo: repo, Subdir: dir}
}

// FromInstalled returns the GitHub source herdr recorded for an installed
// plugin, or false for a locally linked one.
func FromInstalled(info herdr.InstalledPluginInfo) (GitHub, bool) {
	src, ok := info.Source.Get()
	if !ok || src.Kind.ValueOrZero() != herdr.PluginSourceKindGithub {
		return GitHub{}, false
	}
	owner, repo := src.Owner.ValueOrZero(), src.Repo.ValueOrZero()
	if owner == "" || repo == "" {
		return GitHub{}, false
	}
	return GitHub{Owner: owner, Repo: repo, Subdir: src.Subdir.ValueOrZero()}, true
}

// String is the shorthand herdr install and uninstall accept.
func (g GitHub) String() string {
	if g.Subdir == "" {
		return g.Owner + "/" + g.Repo
	}
	return g.Owner + "/" + g.Repo + "/" + g.Subdir
}

// Repository is "owner/repo" without the subdirectory.
func (g GitHub) Repository() string { return g.Owner + "/" + g.Repo }

// CloneURL is the remote herdr clones from.
func (g GitHub) CloneURL() string {
	return "https://github.com/" + g.Owner + "/" + g.Repo + ".git"
}

// WebURL links to the plugin directory on GitHub.
func (g GitHub) WebURL() string {
	u := "https://github.com/" + g.Owner + "/" + g.Repo
	if g.Subdir != "" {
		u += "/tree/HEAD/" + g.Subdir
	}
	return u
}

// ManifestURL is the raw manifest at ref, a branch, tag or commit.
func (g GitHub) ManifestURL(ref string) string {
	p := ManifestFile
	if g.Subdir != "" {
		p = g.Subdir + "/" + ManifestFile
	}
	return g.RawURL(ref, p)
}

// RawURL is the raw file at path, relative to the repository root, at ref.
func (g GitHub) RawURL(ref, p string) string {
	if ref == "" {
		ref = "HEAD"
	}
	// A branch name may contain slashes, which raw.githubusercontent.com
	// expects unescaped.
	segments := strings.Split(ref, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return "https://raw.githubusercontent.com/" + g.Owner + "/" + g.Repo + "/" + strings.Join(segments, "/") + "/" + p
}
