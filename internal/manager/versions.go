package manager

import (
	"context"
	"errors"
	"fmt"

	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-plugin-manager/internal/source"
	"github.com/vika2603/herdr-plugin-manager/internal/updates"
)

// TrackingKind is how an installed plugin is updated, which follows from the
// ref herdr recorded for it.
type TrackingKind string

// Tracking kinds.
const (
	// TrackRelease follows newer release tags.
	TrackRelease TrackingKind = "release"
	// TrackRef follows a branch, or a tag that is not a release, when it moves.
	TrackRef TrackingKind = "ref"
	// TrackDefault follows the default branch.
	TrackDefault TrackingKind = "default-branch"
	// TrackPinned is a commit pin, which has no updates.
	TrackPinned TrackingKind = "pinned"
	// TrackLocal is a linked plugin, which this manager does not update.
	TrackLocal TrackingKind = "local"
)

// Tracking is how an installed plugin is updated.
type Tracking struct {
	Kind TrackingKind
	// Ref is the ref it was installed from and Commit what that resolved to.
	Ref    string
	Commit string
}

// TrackingOf reads p's tracking from its record.
func TrackingOf(p herdr.InstalledPluginInfo) Tracking {
	s := StateOf(p)
	if s.Source == "" {
		return Tracking{Kind: TrackLocal}
	}
	return TrackingAt(s.Ref, s.Commit)
}

// TrackingAt is the tracking of a GitHub install from ref, which resolved to
// commit.
func TrackingAt(ref, commit string) Tracking {
	t := Tracking{Ref: ref, Commit: commit}
	switch {
	case updates.IsCommit(ref):
		t.Kind = TrackPinned
	case ref == "":
		t.Kind = TrackDefault
	case updates.IsRelease(ref):
		t.Kind = TrackRelease
	default:
		t.Kind = TrackRef
	}
	return t
}

// Describe says how the plugin is updated, such as "follows new releases,
// installed at v1.0.0".
func (t Tracking) Describe() string {
	switch t.Kind {
	case TrackLocal:
		return "linked locally; update its working tree, this manager does not"
	case TrackPinned:
		return "pinned to commit " + shortHash(t.Ref) + "; no updates until it is unpinned"
	case TrackDefault:
		return "follows the default branch"
	case TrackRelease:
		return "follows new releases, installed at " + t.Ref
	case TrackRef:
	}
	return "follows " + t.Ref + " when it moves"
}

// ErrLocal is returned for a version change of a locally linked plugin.
var ErrLocal = errors.New("the plugin is linked locally; change its working tree instead")

// VersionRef is the ref a version change of p follows once it is made. ref
// is the user's choice: the version for a switch, and for an unpin what to
// follow then, where empty follows what an install would pick. A pin holds
// the installed commit, and a reinstall keeps the ref the plugin follows,
// at the installed commit; ReinstallCommit names that commit.
func VersionRef(p herdr.InstalledPluginInfo, kind ChangeKind, ref string) (string, error) {
	t := TrackingOf(p)
	if t.Kind == TrackLocal {
		return "", fmt.Errorf("%s: %w", p.PluginID, ErrLocal)
	}
	switch kind {
	case KindSwitch:
		if ref == "" {
			return "", errors.New("name the version to switch to: a release tag, a branch or a commit")
		}
		return ref, nil
	case KindPin:
		switch {
		case t.Kind == TrackPinned:
			return "", fmt.Errorf("%s is already pinned to commit %s", p.PluginID, shortHash(t.Ref))
		case !updates.IsCommit(t.Commit):
			return "", fmt.Errorf("herdr recorded no commit for %s to pin", p.PluginID)
		}
		return t.Commit, nil
	case KindUnpin:
		if t.Kind != TrackPinned {
			return "", fmt.Errorf("%s is not pinned: it %s", p.PluginID, t.Describe())
		}
		return ref, nil
	case KindReinstall:
		return t.Ref, nil
	case KindInstall, KindUpdate, KindRollback, KindRestore, KindUninstall:
	}
	return "", fmt.Errorf("%s is not a version change", kind)
}

// ReinstallCommit is the commit a reinstall of p installs again, "" when
// herdr recorded none.
func ReinstallCommit(p herdr.InstalledPluginInfo) string {
	return TrackingOf(p).Commit
}

// Versions are what a plugin's repository offers to install.
type Versions struct {
	// Releases are the release tags, the newest first, and DefaultBranch
	// the default branch's name.
	Releases      []string
	DefaultBranch string
}

// Versions lists what src's repository offers.
func (m *Manager) Versions(ctx context.Context, src source.GitHub) (Versions, error) {
	refs, err := m.Git.List(ctx, src.CloneURL())
	if err != nil {
		return Versions{}, err
	}
	return Versions{Releases: refs.Releases(), DefaultBranch: refs.HeadBranch}, nil
}
