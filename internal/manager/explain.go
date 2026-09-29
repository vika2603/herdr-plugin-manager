package manager

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/vika2603/herdr-client/herdr"
	"golang.org/x/mod/semver"

	"github.com/vika2603/herdr-plugin-manager/internal/market"
	"github.com/vika2603/herdr-plugin-manager/internal/safe"
	"github.com/vika2603/herdr-plugin-manager/internal/updates"
)

// Explanation describes a change of version for someone deciding on it: in
// words first, then what the authors wrote about it, then what changes in
// what the plugin runs.
type Explanation struct {
	// Headline says what kind of change it is, such as "New release v1.1.0,
	// replacing v1.0.0" or "New commits on the default branch; the version
	// number stays 0.5.0".
	Headline string
	// From and To are the installed and the new version with the revision
	// each is at.
	From, To string
	// Releases are the release notes of the releases the change brings in,
	// the newest first.
	Releases []market.Release
	// Commits are the commits the change brings in, when no release notes
	// say what changed. Scope qualifies them, as for a plugin in a
	// subdirectory, whose repository's commits are not all its own.
	Commits *market.Comparison
	Scope   string
	// NotesErr is why nothing could be read about the changes.
	NotesErr error
	// Runs lists the differences in what the plugin runs and needs, such as
	// "+ build: make" or "min herdr: 0.8.0 -> 0.9.0". It is empty when they
	// are the same.
	Runs []string
}

// Explain describes the change from installed plugin p to preview.
func (m *Manager) Explain(ctx context.Context, p herdr.InstalledPluginInfo, preview *Preview) Explanation {
	from := StateOf(p)
	mf := preview.Manifest
	e := Explanation{
		Headline: headline(from, preview.Ref, preview.Commit, mf.Version),
		From:     from.Version + " at " + RevisionLabel(from.Ref, from.Commit),
		To:       mf.Version + " at " + RevisionLabel(preview.Ref, preview.Commit),
		Runs:     manifestChanges(p, preview),
	}
	if from.Commit == preview.Commit || m.Market == nil {
		return e
	}
	src := preview.Source
	var errs []error
	if updates.IsRelease(preview.Ref) {
		list, err := m.Market.Releases(ctx, src)
		if err != nil {
			errs = append(errs, err)
		}
		e.Releases = releasesBetween(list, from.Ref, preview.Ref)
	}
	if len(e.Releases) == 0 && updates.IsCommit(from.Commit) {
		cmp, err := m.Market.Compare(ctx, src, from.Commit, preview.Commit)
		if err != nil {
			errs = append(errs, err)
		} else {
			e.Commits = &cmp
			if src.Subdir != "" {
				e.Scope = "commits to the whole repository; the plugin is in " + src.Subdir
			}
		}
	}
	if len(e.Releases) == 0 && e.Commits == nil {
		e.NotesErr = errors.Join(errs...)
		if e.NotesErr == nil {
			e.NotesErr = errors.New("no release notes or commits could be read")
		}
	}
	return e
}

// headline names the kind of change from the installed state to ref at
// commit, whose manifest declares version.
func headline(from State, ref, commit, version string) string {
	versions := func() string {
		if from.Version == version {
			return "the version number stays " + version
		}
		return "version " + from.Version + " -> " + version
	}
	switch {
	case from.Commit == commit && from.Ref == ref:
		return "The installed version again, " + version
	case from.Commit == commit:
		return "Same commit, now followed as " + TrackingAt(ref, commit).Describe()
	case updates.IsRelease(ref) && updates.IsRelease(from.Ref) && ref != from.Ref:
		kind := "New release"
		if semver.Compare(canonicalTag(ref), canonicalTag(from.Ref)) < 0 {
			kind = "Older release"
		}
		s := fmt.Sprintf("%s %s, replacing %s", kind, ref, from.Ref)
		if strings.TrimPrefix(ref, "v") != version {
			s += "; " + versions()
		}
		return s
	case ref == from.Ref:
		return "New commits on " + refName(ref) + "; " + versions()
	}
	return fmt.Sprintf("Switch from %s to %s; %s", refName(from.Ref), refName(ref), versions())
}

func refName(ref string) string {
	switch {
	case ref == "":
		return "the default branch"
	case updates.IsCommit(ref):
		return "commit " + shortHash(ref)
	}
	return ref
}

func canonicalTag(tag string) string {
	if !strings.HasPrefix(tag, "v") {
		return "v" + tag
	}
	return tag
}

// releasesBetween are the releases after from up to and including to, the
// newest first; just to when from is not a release.
func releasesBetween(list []market.Release, from, to string) []market.Release {
	var out []market.Release
	for _, r := range list {
		switch {
		case r.Tag == to:
			out = append(out, r)
		case !updates.IsRelease(from) || !updates.IsRelease(r.Tag) || updates.IsPrerelease(r.Tag) && !updates.IsPrerelease(to):
		case semver.Compare(canonicalTag(r.Tag), canonicalTag(from)) > 0 && semver.Compare(canonicalTag(r.Tag), canonicalTag(to)) < 0:
			out = append(out, r)
		}
	}
	slices.SortStableFunc(out, func(a, b market.Release) int {
		return semver.Compare(canonicalTag(b.Tag), canonicalTag(a.Tag))
	})
	return out
}

// manifestChanges lists what differs between what installed plugin p runs
// and needs and what preview's manifest declares.
func manifestChanges(p herdr.InstalledPluginInfo, preview *Preview) []string {
	mf := preview.Manifest
	var out []string
	if was, now := p.MinHerdrVersion.ValueOrZero(), mf.MinHerdrVersion; was != now {
		out = append(out, "min herdr: "+orNone(was)+" -> "+orNone(now))
	}
	var nowPlatforms []string
	for _, pl := range mf.Platforms {
		nowPlatforms = append(nowPlatforms, string(pl))
	}
	var wasPlatforms []string
	for _, pl := range p.Platforms.ValueOrZero() {
		wasPlatforms = append(wasPlatforms, string(pl))
	}
	if was, now := strings.Join(wasPlatforms, ", "), strings.Join(nowPlatforms, ", "); was != now {
		out = append(out, "platforms: "+orAny(was)+" -> "+orAny(now))
	}
	was, now := installedEntries(p), manifestEntries(preview)
	for _, e := range now {
		if !slices.Contains(was, e) {
			out = append(out, "+ "+e)
		}
	}
	for _, e := range was {
		if !slices.Contains(now, e) {
			out = append(out, "- "+e)
		}
	}
	for i := range out {
		out[i] = safe.Line(out[i])
	}
	return out
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func orAny(s string) string {
	if s == "" {
		return "any"
	}
	return s
}

// installedEntries are what installed plugin p runs, one line each.
func installedEntries(p herdr.InstalledPluginInfo) []string {
	var out []string
	for _, b := range p.Build.ValueOrZero() {
		out = append(out, "build: "+Command(b.Command))
	}
	for _, s := range p.Startup.ValueOrZero() {
		out = append(out, "startup: "+Command(s.Command))
	}
	for _, e := range p.Events.ValueOrZero() {
		out = append(out, "event "+e.On+": "+Command(e.Command))
	}
	for _, a := range p.Actions.ValueOrZero() {
		out = append(out, "action "+a.ID+": "+Command(a.Command))
	}
	for _, pn := range p.Panes.ValueOrZero() {
		out = append(out, "pane "+pn.ID+": "+Command(pn.Command))
	}
	for _, l := range p.LinkHandlers.ValueOrZero() {
		out = append(out, "link handler "+l.ID+": "+l.Pattern+" -> "+l.Action)
	}
	return out
}

// manifestEntries are what preview's manifest runs, as installedEntries
// lists them.
func manifestEntries(preview *Preview) []string {
	mf := preview.Manifest
	var out []string
	for _, b := range mf.Build {
		out = append(out, "build: "+Command(b.Command))
	}
	for _, s := range mf.Startup {
		out = append(out, "startup: "+Command(s.Command))
	}
	for _, e := range mf.Events {
		out = append(out, "event "+e.On+": "+Command(e.Command))
	}
	for _, a := range mf.Actions {
		out = append(out, "action "+a.ID+": "+Command(a.Command))
	}
	for _, pn := range mf.Panes {
		out = append(out, "pane "+pn.ID+": "+Command(pn.Command))
	}
	for _, l := range mf.LinkHandlers {
		out = append(out, "link handler "+l.ID+": "+l.Pattern+" -> "+l.Action)
	}
	return out
}

// Sections lays the explanation out for the command line.
func (e Explanation) Sections() []Section {
	out := []Section{{Title: "Change", Lines: []string{e.Headline, "from: " + e.From, "to: " + e.To}}}
	switch {
	case len(e.Releases) > 0:
		for _, r := range e.Releases {
			title := r.Tag
			if r.Name != "" && r.Name != r.Tag {
				title += " " + r.Name
			}
			var lines []string
			notes := strings.TrimSpace(r.Notes)
			if notes == "" {
				lines = []string{"(no release notes)"}
			} else {
				lines = strings.Split(notes, "\n")
			}
			out = append(out, Section{Title: "Release " + title, Note: releaseDate(r.PublishedAt), Lines: lines})
		}
	case e.Commits != nil:
		out = append(out, Section{Title: "Commits", Note: e.CommitsNote(), Lines: e.CommitLines(20)})
	case e.NotesErr != nil:
		out = append(out, Section{Title: "What changed", Lines: []string{"not known: " + e.NotesErr.Error()}})
	}
	runs := e.Runs
	if len(runs) == 0 {
		runs = []string{"no change to what it runs or needs"}
	}
	return append(out, Section{Title: "Manifest changes", Lines: runs})
}

func releaseDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

// CommitsNote counts the commits, qualified by their scope.
func (e Explanation) CommitsNote() string {
	c := e.Commits
	var note string
	switch c.Status {
	case "behind":
		note = "the new commit is older than the installed one"
	case "diverged":
		note = fmt.Sprintf("%d new; the branch history was rewritten, so some installed commits are not in it", c.Total)
	default:
		note = fmt.Sprintf("%d new", c.Total)
	}
	if e.Scope != "" {
		note += "; " + e.Scope
	}
	return note
}

// CommitLines are the titles of up to limit commits, the newest first, with
// a count of the rest.
func (e Explanation) CommitLines(limit int) []string {
	c := e.Commits
	var out []string
	for i, cm := range c.Commits {
		if i == limit {
			break
		}
		line := cm.Title
		if cm.Author != "" {
			line += " (" + cm.Author
			if !cm.Date.IsZero() {
				line += ", " + cm.Date.Format("2006-01-02")
			}
			line += ")"
		}
		out = append(out, line)
	}
	if rest := c.Total - len(out); rest > 0 {
		out = append(out, fmt.Sprintf("… and %d more: %s", rest, c.URL))
	}
	if len(out) == 0 {
		out = []string{"(none)"}
	}
	return out
}
