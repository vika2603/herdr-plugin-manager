// Package updates decides whether a GitHub-installed plugin has a newer
// revision upstream.
//
// herdr records the ref a plugin was installed from (requested_ref, empty for
// the default branch) and the commit it resolved to. What counts as newer
// depends on that ref:
//
//   - a semantic version tag is compared with the highest release tag;
//   - a full commit hash is a pin and never has an update;
//   - a branch, another tag, or no ref at all has an update when the ref now
//     resolves to a different commit.
//
// Updating means reinstalling with the returned target ref, because herdr has
// no update command of its own.
package updates

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-plugin-manager/internal/source"
)

// Kind classifies a check result.
type Kind string

// Check outcomes.
const (
	UpToDate  Kind = "up-to-date"
	Available Kind = "available"
	Pinned    Kind = "pinned"
	Local     Kind = "local"
)

// Result is the outcome of checking one plugin.
type Result struct {
	PluginID string
	Kind     Kind
	// Source is the GitHub source; zero for a local plugin.
	Source source.GitHub
	// CurrentRef is the ref the plugin was installed from, empty for the
	// default branch, and CurrentCommit what it resolved to.
	CurrentRef    string
	CurrentCommit string
	// TargetRef is what to reinstall with: a newer tag, or the same ref when
	// it moved. TargetCommit is the commit TargetRef resolves to now.
	TargetRef    string
	TargetCommit string
}

// Describe is a short human-readable description of the result.
func (r Result) Describe() string {
	switch r.Kind {
	case Available:
		if r.TargetRef != r.CurrentRef {
			return fmt.Sprintf("%s -> %s", refLabel(r.CurrentRef, r.CurrentCommit), r.TargetRef)
		}
		branch := r.CurrentRef
		if branch == "" {
			branch = "the default branch"
		}
		return fmt.Sprintf("new commits on %s (%s -> %s)", branch, short(r.CurrentCommit), short(r.TargetCommit))
	case Pinned:
		return "pinned to " + short(r.CurrentRef)
	case Local:
		return "linked locally"
	default:
		return "up to date"
	}
}

func refLabel(ref, commit string) string {
	if ref != "" {
		return ref
	}
	if commit != "" {
		return "default branch @ " + short(commit)
	}
	return "default branch"
}

func short(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

// Refs is what `git ls-remote` reports for a repository: the default branch
// head, branches and tags. Annotated tags are resolved to the commit they
// point at.
type Refs struct {
	Head string
	// HeadBranch is the name of the default branch, when the remote says.
	HeadBranch string
	Branches   map[string]string
	Tags       map[string]string
}

// Releases lists the release tags, the newest version first. Pre-releases
// are included.
func (r Refs) Releases() []string {
	type release struct{ tag, version string }
	var out []release
	for name := range r.Tags {
		if v, ok := releaseVersion(name); ok {
			out = append(out, release{name, v})
		}
	}
	slices.SortFunc(out, func(a, b release) int {
		return cmp.Or(semver.Compare(b.version, a.version), strings.Compare(a.tag, b.tag))
	})
	tags := make([]string, len(out))
	for i, r := range out {
		tags[i] = r.tag
	}
	return tags
}

// IsRelease reports whether tag is a release tag, such as v1.2.3, which a
// plugin installed from it follows to newer releases.
func IsRelease(tag string) bool {
	_, ok := releaseVersion(tag)
	return ok
}

// IsPrerelease reports whether tag is a release tag for a pre-release, such
// as v1.0.0-rc.1.
func IsPrerelease(tag string) bool {
	v, ok := releaseVersion(tag)
	return ok && semver.Prerelease(v) != ""
}

// LatestRelease is the release tag with the highest version, leaving out
// pre-releases, or "" when there is none.
func (r Refs) LatestRelease() string {
	tag, _ := latestRelease(r.Tags, false)
	return tag
}

// Resolve returns the commit ref points at: the default branch for an empty
// ref, a full commit hash as itself, otherwise a branch or a tag.
func (r Refs) Resolve(ref string) (string, bool) {
	switch {
	case ref == "":
		return r.Head, r.Head != ""
	case IsCommit(ref):
		return ref, true
	case r.Branches[ref] != "":
		return r.Branches[ref], true
	case r.Tags[ref] != "":
		return r.Tags[ref], true
	}
	return "", false
}

// IsCommit reports whether ref is a full commit hash, which herdr records as
// a pin.
func IsCommit(ref string) bool { return commitPattern.MatchString(ref) }

// Lister lists the refs of a remote repository.
type Lister interface {
	List(ctx context.Context, cloneURL string) (Refs, error)
}

// DirComparer tells whether one directory of a remote repository differs
// between two commits.
type DirComparer interface {
	SameDir(ctx context.Context, cloneURL, dir, a, b string) (bool, error)
}

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Check compares one installed plugin with its remote.
func Check(ctx context.Context, lister Lister, info herdr.InstalledPluginInfo) (Result, error) {
	res := Result{PluginID: info.PluginID}
	src, ok := source.FromInstalled(info)
	if !ok {
		res.Kind = Local
		return res, nil
	}
	recorded := info.Source.ValueOrZero()
	res.Source = src
	res.CurrentRef = recorded.RequestedRef.ValueOrZero()
	res.CurrentCommit = recorded.ResolvedCommit.ValueOrZero()

	if IsCommit(res.CurrentRef) {
		res.Kind = Pinned
		return res, nil
	}

	refs, err := lister.List(ctx, src.CloneURL())
	if err != nil {
		return res, fmt.Errorf("%s: %w", info.PluginID, err)
	}
	return decide(res, refs)
}

func decide(res Result, refs Refs) (Result, error) {
	if current, ok := releaseVersion(res.CurrentRef); ok {
		if _, isTag := refs.Tags[res.CurrentRef]; isTag {
			latest, latestVersion := latestRelease(refs.Tags, semver.Prerelease(current) != "")
			if latest != "" && semver.Compare(latestVersion, current) > 0 {
				res.Kind = Available
				res.TargetRef = latest
				res.TargetCommit = refs.Tags[latest]
				return res, nil
			}
			res.Kind = UpToDate
			return res, nil
		}
	}

	var now string
	switch {
	case res.CurrentRef == "":
		now = refs.Head
	case refs.Branches[res.CurrentRef] != "":
		now = refs.Branches[res.CurrentRef]
	case refs.Tags[res.CurrentRef] != "":
		now = refs.Tags[res.CurrentRef]
	default:
		return res, fmt.Errorf("%s: ref %q no longer exists upstream", res.PluginID, res.CurrentRef)
	}
	res.TargetRef = res.CurrentRef
	res.TargetCommit = now
	if now != res.CurrentCommit {
		res.Kind = Available
	} else {
		res.Kind = UpToDate
	}
	return res, nil
}

// releaseVersion reads a tag such as "v1.2.3" or "1.2.3" as a semantic
// version in the canonical "v" form. Shorthand such as "v1" or "v1.2" is
// valid semver to x/mod but is usually a moving major-version tag, not a
// release, so it is not accepted.
func releaseVersion(tag string) (string, bool) {
	if tag == "" {
		return "", false
	}
	v := tag
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return "", false
	}
	if core, _, _ := strings.Cut(v, "+"); semver.Canonical(v) != core {
		return "", false
	}
	return v, true
}

// latestRelease returns the tag with the highest semantic version. Pre-release
// tags are considered only when the installed tag is one itself.
func latestRelease(tags map[string]string, prerelease bool) (tag, version string) {
	for name := range tags {
		v, ok := releaseVersion(name)
		if !ok || (!prerelease && semver.Prerelease(v) != "") {
			continue
		}
		c := semver.Compare(v, version)
		// Equal versions such as "v1.0.0" and "1.0.0": prefer the name that
		// sorts first so the choice does not depend on map order.
		if version == "" || c > 0 || (c == 0 && name < tag) {
			tag, version = name, v
		}
	}
	return tag, version
}

// listTimeout bounds one `git ls-remote`, so an unresponsive remote fails
// its check instead of holding up the others.
const listTimeout = 30 * time.Second

// fetchTimeout bounds the tree fetch of SameDir.
const fetchTimeout = time.Minute

// Git reads remotes with the git command, which herdr already requires for
// installs.
type Git struct {
	// Path is the git executable; empty means "git" on PATH.
	Path string
}

// List runs `git ls-remote`.
func (g Git) List(ctx context.Context, cloneURL string) (Refs, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	out, err := g.run(ctx, "ls-remote", "--symref", cloneURL)
	if err != nil {
		return Refs{}, err
	}
	return ParseLsRemote(out), nil
}

// SameDir reports whether dir holds the same files at commits a and b. It
// fetches the two commits' trees, without file contents, into a scratch
// repository that it removes afterwards. A dir missing at either commit is
// an error.
func (g Git) SameDir(ctx context.Context, cloneURL, dir, a, b string) (bool, error) {
	tmp, err := os.MkdirTemp("", "hpm-git-")
	if err != nil {
		return false, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	if _, err := g.run(ctx, "init", "-q", "--bare", tmp); err != nil {
		return false, err
	}
	if _, err := g.run(ctx, "-C", tmp, "fetch", "-q", "--depth=1", "--filter=blob:none", "--no-tags", cloneURL, a, b); err != nil {
		return false, err
	}
	var trees [2]string
	for i, commit := range []string{a, b} {
		out, err := g.run(ctx, "-C", tmp, "rev-parse", "--verify", "-q", commit+":"+dir)
		if err != nil {
			return false, fmt.Errorf("%s has no %s at %.12s", cloneURL, dir, commit)
		}
		trees[i] = strings.TrimSpace(out)
	}
	return trees[0] == trees[1], nil
}

// run runs git and returns its output. Terminal prompts are disabled so that
// a private or missing repository fails instead of waiting for credentials.
func (g Git) run(ctx context.Context, args ...string) (string, error) {
	bin := g.Path
	if bin == "" {
		bin = "git"
	}
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // git with fixed subcommands; URLs come from source.GitHub and commits are checked hashes.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	// git hands a transfer to git-remote-https, which inherits the output
	// pipe; without a delay, Wait could outlast the cancellation.
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		name := "git " + args[0]
		if args[0] == "-C" {
			name = "git " + args[2]
		}
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("%s: %s", name, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return string(out), nil
}

// ParseLsRemote reads `git ls-remote --symref` output.
func ParseLsRemote(out string) Refs {
	refs := Refs{Branches: map[string]string{}, Tags: map[string]string{}}
	peeled := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		sha, name, ok := strings.Cut(scanner.Text(), "\t")
		if !ok {
			continue
		}
		if target, isSymref := strings.CutPrefix(sha, "ref: "); isSymref {
			if name == "HEAD" {
				refs.HeadBranch = strings.TrimPrefix(target, "refs/heads/")
			}
			continue
		}
		switch {
		case name == "HEAD":
			refs.Head = sha
		case strings.HasPrefix(name, "refs/heads/"):
			refs.Branches[strings.TrimPrefix(name, "refs/heads/")] = sha
		case strings.HasPrefix(name, "refs/tags/"):
			tag := strings.TrimPrefix(name, "refs/tags/")
			if base, ok := strings.CutSuffix(tag, "^{}"); ok {
				peeled[base] = sha
			} else {
				refs.Tags[tag] = sha
			}
		}
	}
	// herdr records the commit a checkout resolved to, which for an
	// annotated tag is the peeled commit rather than the tag object.
	maps.Copy(refs.Tags, peeled)
	return refs
}
