package manager

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/vika2603/herdr-client/herdr"
	"github.com/vika2603/herdr-client/plugin/manifest"

	"github.com/vika2603/herdr-plugin-manager/internal/safe"
	"github.com/vika2603/herdr-plugin-manager/internal/source"
)

// Preview is what an install would bring in, read from the manifest at the
// ref it would check out.
type Preview struct {
	Source source.GitHub
	Ref    string
	// Commit is what Ref resolved to when the manifest was read. Install and
	// Update refuse to go ahead when Ref has since moved, so what is
	// installed is what was reviewed.
	Commit   string
	Manifest *manifest.Manifest
	Warnings []string
	// Problems are reasons the plugin cannot be installed or run here.
	Problems []string
	// Existing is the installed plugin with the same id, if any.
	Existing *herdr.InstalledPluginInfo
	// Platform is this machine's platform. Commands declared only for other
	// platforms are left out of Sections.
	Platform string
}

// RequireID records a problem when the manifest's id is not id: reinstalling
// a plugin whose id changed upstream would register a second plugin instead
// of updating the first.
func (p *Preview) RequireID(id string) {
	if p.Manifest.ID != id {
		p.Problems = append(p.Problems, fmt.Sprintf("the plugin id changed upstream from %s to %s; uninstall and install it again", id, p.Manifest.ID))
	}
}

// Section is a titled group of preview lines.
type Section struct {
	Title string
	Lines []string
}

// Sections lays out the preview. The commands come first after the summary
// because they are what runs on this machine: build commands during install,
// startup commands in every session.
func (p *Preview) Sections() []Section {
	m := p.Manifest
	ref := p.Ref
	if ref == "" {
		ref = "default branch"
	}
	summary := []string{
		fmt.Sprintf("%s %s (%s)", m.Name, m.Version, m.ID),
		"source: " + p.Source.String() + " @ " + ref + shortCommit(p.Commit),
		"link: " + p.Source.WebURL(),
	}
	if m.Description != "" {
		summary = append(summary, m.Description)
	}
	summary = append(summary, "platforms: "+platformList(m.Platforms), "min herdr: "+m.MinHerdrVersion)
	if p.Existing != nil {
		summary = append(summary, fmt.Sprintf("replaces installed %s from %s", p.Existing.Version, SourceLabel(*p.Existing)))
	}
	out := []Section{{Title: "Plugin", Lines: summary}}
	if len(p.Problems) > 0 {
		out = append(out, Section{Title: "Problems", Lines: p.Problems})
	}
	if len(p.Warnings) > 0 {
		out = append(out, Section{Title: "Manifest warnings", Lines: p.Warnings})
	}

	entries, hidden := p.entrypoints()
	if hidden > 0 {
		out[0].Lines = append(out[0].Lines, fmt.Sprintf("%d entries for other platforms not shown", hidden))
	}
	out = append(out, entries...)
	// The manifest is the plugin author's text; make every line printable.
	for i := range out {
		for j, line := range out[i].Lines {
			out[i].Lines[j] = safe.Line(line)
		}
	}
	return out
}

// entrypoints lists what the manifest runs, one section per kind, leaving out
// entries declared only for other platforms and counting them.
func (p *Preview) entrypoints() (sections []Section, hidden int) {
	m := p.Manifest
	applies := func(platforms []manifest.Platform) bool {
		if p.Platform == "" || len(platforms) == 0 || slices.Contains(platforms, manifest.Platform(p.Platform)) {
			return true
		}
		hidden++
		return false
	}
	var build, startup, events, actions, panes, links []string
	for _, b := range m.Build {
		if applies(b.Platforms) {
			build = append(build, Command(b.Command))
		}
	}
	for _, s := range m.Startup {
		if applies(s.Platforms) {
			startup = append(startup, Command(s.Command))
		}
	}
	for _, e := range m.Events {
		if applies(e.Platforms) {
			events = append(events, e.On+": "+Command(e.Command))
		}
	}
	for _, a := range m.Actions {
		if applies(a.Platforms) {
			actions = append(actions, fmt.Sprintf("%s (%s): %s", a.ID, a.Title, Command(a.Command)))
		}
	}
	for _, pn := range m.Panes {
		if applies(pn.Platforms) {
			placement := cmp.Or(string(pn.Placement), string(manifest.PlacementOverlay))
			panes = append(panes, fmt.Sprintf("%s (%s, %s): %s", pn.ID, pn.Title, placement, Command(pn.Command)))
		}
	}
	for _, l := range m.LinkHandlers {
		if applies(l.Platforms) {
			links = append(links, fmt.Sprintf("%s: %s -> action %s", l.ID, l.Pattern, l.Action))
		}
	}
	for _, s := range []Section{
		{"Build commands (run during install)", build},
		{"Startup commands (run in every session)", startup},
		{"Event hooks", events},
		{"Actions", actions},
		{"Panes", panes},
		{"Link handlers", links},
	} {
		if len(s.Lines) > 0 {
			sections = append(sections, s)
		}
	}
	return sections, hidden
}

func shortCommit(commit string) string {
	if len(commit) < 12 || commit == "" {
		return ""
	}
	return " (" + commit[:12] + ")"
}

// Command renders an argv the way a shell would read it back.
func Command(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		// Quoting also escapes anything unprintable, so a carriage return or
		// an escape sequence in an argument cannot change how the rest of
		// the command reads.
		unprintable := strings.ContainsFunc(arg, func(r rune) bool { return !unicode.IsPrint(r) })
		if arg == "" || unprintable || strings.ContainsAny(arg, " \t\n'\"\\$`*?[]{}()<>|&;#~") {
			quoted[i] = strconv.Quote(arg)
		} else {
			quoted[i] = arg
		}
	}
	return strings.Join(quoted, " ")
}

func platformList(platforms []manifest.Platform) string {
	if len(platforms) == 0 {
		return "undeclared"
	}
	names := make([]string, len(platforms))
	for i, p := range platforms {
		names[i] = string(p)
	}
	return strings.Join(names, ", ")
}

// SourceLabel describes where an installed plugin came from.
func SourceLabel(p herdr.InstalledPluginInfo) string {
	if src, ok := source.FromInstalled(p); ok {
		s := src.String()
		info := p.Source.ValueOrZero()
		if ref := info.RequestedRef.ValueOrZero(); ref != "" {
			return s + "@" + safe.Line(ref)
		}
		if commit := info.ResolvedCommit.ValueOrZero(); len(commit) >= 7 {
			return s + "@" + commit[:7]
		}
		return s
	}
	return "local:" + safe.Line(p.PluginRoot)
}

// LogHeader summarises one command log entry on a line.
func LogHeader(l herdr.PluginCommandLogInfo) string {
	started := time.UnixMilli(int64(l.StartedUnixMs)).Format(time.DateTime) //nolint:gosec // Milliseconds since 1970 fit int64 for 292 million years.
	what := l.ActionID.ValueOrZero()
	if what == "" {
		what = l.Event.ValueOrZero()
	}
	status := string(l.Status)
	if code, ok := l.ExitCode.Get(); ok {
		status += fmt.Sprintf(" (exit %d)", code)
	}
	line := fmt.Sprintf("%s  %-9s %s  %s", started, status, what, Command(l.Command))
	if e := l.Error.ValueOrZero(); e != "" {
		line += "  error: " + e
	}
	return line
}
