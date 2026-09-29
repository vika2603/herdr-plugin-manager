package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-plugin-manager/internal/safe"
	"github.com/vika2603/herdr-plugin-manager/internal/source"
	"github.com/vika2603/herdr-plugin-manager/internal/updates"
)

// ExportFormat names the file Export writes, and ExportVersion is the
// version of its layout that this manager reads and writes.
const (
	ExportFormat  = "hpm-plugins"
	ExportVersion = 1
)

// Source kinds in an export. Any other kind is one herdr reported that this
// manager cannot install.
const (
	ExportGitHub = "github"
	ExportLocal  = "local"
)

// Export is the list of installed plugins that Restore brings back
// elsewhere.
type Export struct {
	Format       string           `json:"format"`
	Version      int              `json:"version"`
	ExportedAt   time.Time        `json:"exported_at"`
	HerdrVersion string           `json:"herdr_version,omitempty"`
	Plugins      []ExportedPlugin `json:"plugins"`
}

// ExportedPlugin is one installed plugin as exported.
type ExportedPlugin struct {
	ID string `json:"id"`
	// Name and Version are what the manifest declared, for reading.
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	// Enabled is a pointer so that a file without it is rejected rather
	// than read as disabled.
	Enabled *bool `json:"enabled"`
	// Kind is ExportGitHub, ExportLocal, or another kind herdr reported.
	Kind string `json:"kind"`
	// Source is owner/repo[/subdir] for a GitHub install. Ref is the ref it
	// follows, empty for the default branch, and Commit the commit
	// installed. Tracking is how Ref is followed; restore checks it
	// against Ref.
	Source   string       `json:"source,omitempty"`
	Ref      string       `json:"ref,omitempty"`
	Commit   string       `json:"commit,omitempty"`
	Tracking TrackingKind `json:"tracking,omitempty"`
	// Root is the directory a plugin that is not a GitHub install was
	// registered from, on the machine that exported it.
	Root string `json:"root,omitempty"`
}

// IsEnabled reports the exported enabled state.
func (p ExportedPlugin) IsEnabled() bool { return p.Enabled != nil && *p.Enabled }

// state is the exported plugin as a State.
func (p ExportedPlugin) state() State {
	return State{Version: p.Version, Source: p.Source, Root: p.Root, Ref: p.Ref, Commit: p.Commit, Enabled: p.IsEnabled()}
}

// Export lists the installed plugins with the ref each follows. The refs
// this manager keeps for the plugins it pinned must be readable: without
// them such a plugin would be exported as a commit pin.
func (m *Manager) Export(ctx context.Context) (*Export, error) {
	plugins, err := m.installedFollowed(ctx)
	if err != nil {
		return nil, err
	}
	exp := &Export{
		Format: ExportFormat, Version: ExportVersion, ExportedAt: time.Now().UTC().Truncate(time.Second),
		HerdrVersion: m.HerdrVersion(ctx), Plugins: make([]ExportedPlugin, 0, len(plugins)),
	}
	for _, p := range plugins {
		exp.Plugins = append(exp.Plugins, exportedOf(p))
	}
	return exp, nil
}

// installedFollowed is Installed, failing when the kept refs cannot be read
// instead of listing the pins as herdr records them.
func (m *Manager) installedFollowed(ctx context.Context) ([]herdr.InstalledPluginInfo, error) {
	plugins, err := m.installedRaw(ctx)
	if err != nil {
		return nil, err
	}
	follows, err := m.History.follows()
	if err != nil {
		return nil, fmt.Errorf("the refs this manager keeps for the plugins it pinned could not be read: %w", err)
	}
	applyFollows(plugins, follows)
	return plugins, nil
}

func exportedOf(p herdr.InstalledPluginInfo) ExportedPlugin {
	enabled := p.Enabled
	e := ExportedPlugin{ID: p.PluginID, Name: p.Name, Version: p.Version, Enabled: &enabled}
	if src, ok := source.FromInstalled(p); ok {
		info := p.Source.ValueOrZero()
		e.Kind, e.Source = ExportGitHub, src.String()
		e.Ref, e.Commit = info.RequestedRef.ValueOrZero(), info.ResolvedCommit.ValueOrZero()
		e.Tracking = TrackingAt(e.Ref, e.Commit).Kind
		return e
	}
	e.Kind, e.Root = string(p.Source.ValueOrZero().Kind.ValueOrZero()), p.PluginRoot
	if e.Kind == "" {
		e.Kind = ExportLocal
	}
	return e
}

// WriteExport writes exp as indented JSON.
func WriteExport(w io.Writer, exp *Export) error {
	data, err := json.MarshalIndent(exp, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

// ReadExport reads a file Export wrote. A file of another format or a newer
// version, a field this version does not know, or a plugin without an id or
// enabled state is refused as a whole, so nothing in it is misread. Entries
// that are well formed but cannot be installed are left to PlanRestore.
func ReadExport(r io.Reader) (*Export, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var head struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("not an hpm plugin export: %w", err)
	}
	switch {
	case head.Format != ExportFormat:
		return nil, fmt.Errorf("not an hpm plugin export: format is %q, want %q", safe.Line(head.Format), ExportFormat)
	case head.Version > ExportVersion:
		return nil, fmt.Errorf("the export has format version %d, from a newer hpm; this hpm reads version %d", head.Version, ExportVersion)
	case head.Version != ExportVersion:
		return nil, fmt.Errorf("the export has unknown format version %d", head.Version)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var exp Export
	if err := dec.Decode(&exp); err != nil {
		return nil, fmt.Errorf("read the export: %w", err)
	}
	seen := map[string]bool{}
	for i, p := range exp.Plugins {
		switch {
		case p.ID == "":
			return nil, fmt.Errorf("plugin %d in the export has no id", i+1)
		case seen[p.ID]:
			return nil, fmt.Errorf("the export lists %s more than once", safe.Line(p.ID))
		case p.Enabled == nil:
			return nil, fmt.Errorf("%s in the export does not say whether it is enabled", safe.Line(p.ID))
		case p.Kind == "":
			return nil, fmt.Errorf("%s in the export does not say where it was installed from", safe.Line(p.ID))
		}
		seen[p.ID] = true
	}
	return &exp, nil
}

// RestoreAction is what restoring one exported plugin does here.
type RestoreAction string

// Restore actions. Install and Change run herdr; Enable and Disable only
// change the enabled state. The others do nothing and say why.
const (
	// RestoreInstall installs a plugin that is not installed here.
	RestoreInstall RestoreAction = "install"
	// RestoreChange reinstalls a plugin installed here from the same
	// source at another commit, or following another ref.
	RestoreChange RestoreAction = "change"
	// RestoreEnable and RestoreDisable change only the enabled state of a
	// plugin installed here as exported.
	RestoreEnable  RestoreAction = "enable"
	RestoreDisable RestoreAction = "disable"
	// RestoreUnchanged is a plugin already installed here as exported.
	RestoreUnchanged RestoreAction = "unchanged"
	// RestoreConflict is a plugin installed here under the same id from
	// another source, or linked locally; it is left alone.
	RestoreConflict RestoreAction = "conflict"
	// RestoreLocal is a plugin linked locally where it was exported, which
	// only herdr plugin link can bring back.
	RestoreLocal RestoreAction = "local"
	// RestoreUnsupported is an entry that names no GitHub source and
	// commit this manager can install.
	RestoreUnsupported RestoreAction = "unsupported"
)

// RestoreItem is what restoring one exported plugin would do.
type RestoreItem struct {
	Plugin ExportedPlugin
	Action RestoreAction
	// Current is the plugin installed here under the same id, nil when
	// there is none.
	Current *herdr.InstalledPluginInfo
	// Target is what Install and Change ask herdr for: the exported commit,
	// followed as the exported ref, enabled or disabled as exported.
	Target Target
	// Preview is the manifest at the exported commit, and Explain what
	// changes from Current, for a Change.
	Preview *Preview
	Explain *Explanation
	// Notes are facts about the item that do not stop it, such as the ref
	// having moved on since the export.
	Notes []string
	// Blocker is why the item is not restored, empty when it can be or
	// needs nothing.
	Blocker string
}

// Runs reports whether restoring the item changes anything here.
func (it RestoreItem) Runs() bool {
	switch it.Action {
	case RestoreInstall, RestoreChange, RestoreEnable, RestoreDisable:
		return it.Blocker == ""
	case RestoreUnchanged, RestoreConflict, RestoreLocal, RestoreUnsupported:
	}
	return false
}

// Describe says on one line what restoring the item does, or why it does
// not. The text is printable.
func (it RestoreItem) Describe() string {
	want := it.Plugin.state()
	if it.Preview != nil {
		want.Version = it.Preview.Manifest.Version
	}
	if want.Version == "" {
		want.Version = "unknown version"
	}
	var s string
	switch it.Action {
	case RestoreInstall:
		s = "install " + want.String() + "; " + TrackingAt(want.Ref, want.Commit).Describe()
	case RestoreChange:
		s = "change " + StateOf(*it.Current).String() + " -> " + want.String()
	case RestoreEnable, RestoreDisable:
		s = string(it.Action) + ": installed here as exported, " + StateOf(*it.Current).String()
	case RestoreUnchanged:
		s = "unchanged: already installed as exported, " + want.String()
	case RestoreConflict:
		return safe.Line("conflict: " + it.Blocker)
	case RestoreLocal, RestoreUnsupported:
		return safe.Line("cannot restore: " + it.Blocker)
	}
	if it.Blocker != "" {
		s += "; cannot restore: " + it.Blocker
	}
	return safe.Line(s)
}

// RestorePlan is what restoring an export would do here.
type RestorePlan struct {
	Items []RestoreItem
	// Left are the exported plugins not asked for, which are not planned.
	Left []string
	// Others are the plugins installed here that the export does not list,
	// which a restore leaves as they are.
	Others []string
}

// PlanRestore works out what restoring exp would do here, for the plugins
// with the given ids or, with none, for all of them, and reads the manifest
// each install would build at the exported commit. Nothing is changed. An
// item that cannot be restored as exported, because of its source, a
// conflict here, its manifest or a missing herdr server, says why and is not
// run; nothing is installed in its place.
func (m *Manager) PlanRestore(ctx context.Context, exp *Export, ids []string, herdrVersion string) (RestorePlan, error) {
	exported := map[string]bool{}
	for _, p := range exp.Plugins {
		exported[p.ID] = true
	}
	for _, id := range ids {
		if !exported[id] {
			return RestorePlan{}, fmt.Errorf("the export does not list %s", id)
		}
	}
	installed, err := m.installedFollowed(ctx)
	if err != nil {
		return RestorePlan{}, err
	}
	var plan RestorePlan
	for _, p := range installed {
		if !exported[p.PluginID] {
			plan.Others = append(plan.Others, p.PluginID)
		}
	}
	var planned []ExportedPlugin
	for _, p := range exp.Plugins {
		if ids == nil || slices.Contains(ids, p.ID) {
			planned = append(planned, p)
		} else {
			plan.Left = append(plan.Left, p.ID)
		}
	}
	serverErr := m.ping(ctx)
	plan.Items = make([]RestoreItem, len(planned))
	sem := make(chan struct{}, checkConcurrency)
	var wg sync.WaitGroup
	for i, p := range planned {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			plan.Items[i] = m.planItem(ctx, p, installed, herdrVersion, serverErr)
		})
	}
	wg.Wait()
	return plan, nil
}

func (m *Manager) planItem(ctx context.Context, p ExportedPlugin, installed []herdr.InstalledPluginInfo, herdrVersion string, serverErr error) RestoreItem {
	it := RestoreItem{Plugin: p}
	for i := range installed {
		if installed[i].PluginID == p.ID {
			current := installed[i]
			it.Current = &current
		}
	}
	src, reason := restorable(p)
	switch {
	case p.Kind == ExportLocal:
		it.Action = RestoreLocal
		it.Blocker = "linked locally from " + p.Root + " where it was exported; hpm cannot restore a link, so link its directory here with herdr plugin link"
		if it.Current != nil {
			it.Blocker += "; installed here as " + StateOf(*it.Current).String()
		}
		return it
	case reason != "":
		it.Action, it.Blocker = RestoreUnsupported, reason
		return it
	}
	it.Target = Target{Source: src, Ref: p.Ref, Commit: p.Commit, Enabled: p.Enabled}
	if it.Current != nil {
		now := StateOf(*it.Current)
		switch {
		case now.Source == "":
			it.Action = RestoreConflict
			it.Blocker = "linked locally here from " + now.Root + "; unlink it to restore it from " + p.Source
			return it
		case now.Source != p.Source:
			it.Action = RestoreConflict
			it.Blocker = "installed here from " + now.Source + ", not " + p.Source + "; uninstall it to restore it from the export"
			return it
		case now.SameRevision(p.state()) && now.Enabled == p.IsEnabled():
			it.Action = RestoreUnchanged
			return it
		case now.SameRevision(p.state()):
			it.Action = RestoreDisable
			if p.IsEnabled() {
				it.Action = RestoreEnable
			}
			if serverErr != nil {
				it.Blocker = "changing whether it is enabled needs a running herdr server; " + serverProblem(serverErr)
			}
			return it
		}
		it.Action = RestoreChange
	} else {
		it.Action = RestoreInstall
	}
	if !p.IsEnabled() && serverErr != nil {
		it.Blocker = ErrKeepDisabled.Error() + "; " + serverProblem(serverErr)
	}
	preview, err := m.PreviewAt(ctx, src, p.Ref, p.Commit, herdrVersion, nil)
	if err != nil {
		it.Blocker = joinReasons(it.Blocker, "the manifest at the exported commit could not be read: "+err.Error())
		return it
	}
	preview.RequireID(p.ID)
	it.Preview = preview
	if len(preview.Problems) > 0 {
		it.Blocker = joinReasons(it.Blocker, strings.Join(preview.Problems, "; "))
	}
	if it.Current != nil {
		e := m.Explain(ctx, *it.Current, preview)
		it.Explain = &e
	}
	it.Notes = m.restoreNotes(ctx, p)
	return it
}

// restorable returns the source of an exported GitHub install, or why the
// entry cannot be installed as exported.
func restorable(p ExportedPlugin) (src source.GitHub, reason string) {
	if p.Kind != ExportGitHub {
		return source.GitHub{}, fmt.Sprintf("herdr recorded its source as %q, which hpm cannot install", p.Kind)
	}
	if p.Source == "" {
		return source.GitHub{}, "herdr recorded no GitHub repository for it"
	}
	src, err := source.Parse(p.Source)
	if err != nil {
		return source.GitHub{}, "its source cannot be read: " + err.Error()
	}
	if src.String() != p.Source {
		return source.GitHub{}, fmt.Sprintf("its source %q is not in owner/repo[/subdir] form", p.Source)
	}
	if !updates.IsCommit(p.Commit) {
		return source.GitHub{}, "the export records no full commit hash, so what was installed is not known"
	}
	if p.Ref != strings.TrimSpace(p.Ref) || strings.HasPrefix(p.Ref, "-") || safe.Line(p.Ref) != p.Ref || strings.ContainsAny(p.Ref, " ~^:?*[\\") {
		return source.GitHub{}, fmt.Sprintf("its ref %q is not a valid git ref", p.Ref)
	}
	if updates.IsCommit(p.Ref) && p.Ref != p.Commit {
		return source.GitHub{}, "it is pinned to one commit and records another as installed"
	}
	if want := TrackingAt(p.Ref, p.Commit).Kind; p.Tracking != "" && p.Tracking != want {
		return source.GitHub{}, fmt.Sprintf("its tracking is %q, but its ref means %q", p.Tracking, want)
	}
	return src, ""
}

// restoreNotes says where the exported ref stands now: a restore installs
// the exported commit, so a ref that has moved on is an update afterwards,
// and a ref that is gone leaves the plugin without updates.
func (m *Manager) restoreNotes(ctx context.Context, p ExportedPlugin) []string {
	if updates.IsCommit(p.Ref) || m.Git == nil {
		return nil
	}
	src, _ := source.Parse(p.Source)
	info := herdr.InstalledPluginInfo{PluginID: p.ID, Source: herdr.Some(herdr.PluginSourceInfo{
		Kind:  herdr.Some(herdr.PluginSourceKindGithub),
		Owner: herdr.Some(src.Owner), Repo: herdr.Some(src.Repo), Subdir: herdr.Some(src.Subdir),
		RequestedRef: herdr.Some(p.Ref), ResolvedCommit: herdr.Some(p.Commit),
	})}
	res, err := m.Check(ctx, info)
	switch {
	case err != nil:
		return []string{"could not check " + refName(p.Ref) + " upstream: " + err.Error()}
	case res.Kind == updates.Available:
		return []string{"installs the exported commit; " + res.Describe() + " is available as an update afterwards"}
	}
	return nil
}

// serverProblem says why no server answered, in short when none is running.
func serverProblem(err error) string {
	if IsServerDown(err) {
		return "no herdr server is running"
	}
	return "the herdr server did not answer: " + err.Error()
}

func joinReasons(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

// RestoreResult is how restoring one item ended.
type RestoreResult string

// Restore results.
const (
	ResultDone        RestoreResult = "done"
	ResultFailed      RestoreResult = "failed"
	ResultCancelled   RestoreResult = "cancelled"
	ResultUnconfirmed RestoreResult = "unconfirmed"
	// ResultChanged is a change refused because the plugin changed after
	// the plan was made; the plan needs reviewing again.
	ResultChanged RestoreResult = "changed since planned"
)

// ResultOf classifies an outcome of Restore.
func ResultOf(o Outcome) RestoreResult {
	switch {
	case o.Err == nil:
		return ResultDone
	case errors.Is(o.Err, ErrCancelled):
		return ResultCancelled
	case errors.Is(o.Err, ErrChanged):
		return ResultChanged
	case o.AfterUnknown:
		return ResultUnconfirmed
	}
	return ResultFailed
}

// Restore carries out one planned item that Runs, as a change of kind
// restore that the history records and rollback undoes; herdr's output goes
// to out. An install or change is an Apply; enabling or disabling changes
// only the enabled state. The change is refused, and says to review it
// again, when the plugin is no longer as it was when the item was planned.
func (m *Manager) Restore(ctx context.Context, it RestoreItem, out io.Writer) Outcome {
	switch it.Action {
	case RestoreInstall, RestoreChange:
		return m.Apply(ctx, Change{Kind: KindRestore, ID: it.Plugin.ID, Current: it.Current, Target: it.Target, Expect: true}, out)
	case RestoreEnable, RestoreDisable:
		return m.setEnabled(ctx, KindRestore, it.Plugin.ID, it.Action == RestoreEnable, it.Current, out)
	case RestoreUnchanged, RestoreConflict, RestoreLocal, RestoreUnsupported:
	}
	o := Outcome{Kind: KindRestore, ID: it.Plugin.ID, Err: fmt.Errorf("%s is not restored: %s", it.Plugin.ID, it.Action)}
	if it.Current != nil {
		s := StateOf(*it.Current)
		o.Before, o.After = &s, &s
	}
	return o
}
