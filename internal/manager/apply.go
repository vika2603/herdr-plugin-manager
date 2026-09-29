package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-plugin-manager/internal/source"
	"github.com/vika2603/herdr-plugin-manager/internal/updates"
)

// ChangeKind names what a change to an installed plugin is for. Every kind
// but uninstall is a herdr install, since herdr has no other way to move a
// plugin to another revision.
type ChangeKind string

// Change kinds.
const (
	KindInstall   ChangeKind = "install"
	KindUpdate    ChangeKind = "update"
	KindSwitch    ChangeKind = "switch"
	KindReinstall ChangeKind = "reinstall"
	KindPin       ChangeKind = "pin"
	KindUnpin     ChangeKind = "unpin"
	KindRollback  ChangeKind = "rollback"
	KindRestore   ChangeKind = "restore"
	KindUninstall ChangeKind = "uninstall"
)

// State is what herdr records about an installed plugin that a change can
// alter.
type State struct {
	Version string `json:"version"`
	// Source is owner/repo[/subdir], empty for a locally linked plugin, whose
	// directory is Root.
	Source string `json:"source,omitempty"`
	Root   string `json:"root,omitempty"`
	// Ref is the ref it was installed from, empty for the default branch, and
	// Commit what that resolved to.
	Ref     string `json:"ref,omitempty"`
	Commit  string `json:"commit,omitempty"`
	Enabled bool   `json:"enabled"`
}

// StateOf is p's state.
func StateOf(p herdr.InstalledPluginInfo) State {
	s := State{Version: p.Version, Enabled: p.Enabled}
	if src, ok := source.FromInstalled(p); ok {
		info := p.Source.ValueOrZero()
		s.Source, s.Ref, s.Commit = src.String(), info.RequestedRef.ValueOrZero(), info.ResolvedCommit.ValueOrZero()
	} else {
		s.Root = p.PluginRoot
	}
	return s
}

// SameRevision reports whether s and o are the same install, whatever their
// enabled state.
func (s State) SameRevision(o State) bool {
	return s.Source == o.Source && s.Root == o.Root && s.Ref == o.Ref && s.Commit == o.Commit
}

// String describes the state on one line, such as
// "1.1.0 at v1.1.0 (83ab6ed1b4d5), enabled".
func (s State) String() string {
	enabled := "enabled"
	if !s.Enabled {
		enabled = "disabled"
	}
	if s.Source == "" {
		return fmt.Sprintf("%s linked from %s, %s", s.Version, s.Root, enabled)
	}
	return fmt.Sprintf("%s at %s, %s", s.Version, RevisionLabel(s.Ref, s.Commit), enabled)
}

// RevisionLabel names a ref and the commit it resolved to, such as
// "v1.1.0 (83ab6ed1b4d5)" or "default branch (83ab6ed1b4d5)".
func RevisionLabel(ref, commit string) string {
	switch {
	case updates.IsCommit(ref):
		return "commit " + shortHash(ref)
	case ref == "" && commit == "":
		return "the default branch"
	case ref == "":
		return "default branch (" + shortHash(commit) + ")"
	case commit == "":
		return ref
	}
	return ref + " (" + shortHash(commit) + ")"
}

func shortHash(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

// Target is what a change installs, and the state it leaves the plugin in.
type Target struct {
	Source source.GitHub
	// Ref is what herdr is asked for, empty for the default branch; Commit is
	// what the preview read at Ref. The install is refused when Ref has moved
	// since, and checked against what herdr installed.
	Ref    string
	Commit string
	// Enabled, when set, is the state to leave the plugin in. Otherwise an
	// installed plugin keeps its state and a new one is enabled, as herdr
	// registers it.
	Enabled *bool
}

func (t Target) record() *TargetRecord {
	return &TargetRecord{Source: t.Source.String(), Ref: t.Ref, Commit: t.Commit}
}

// Change is one install over, or of, plugin ID.
type Change struct {
	Kind ChangeKind
	ID   string
	// Current is the caller's record of the plugin, nil when it is not
	// installed. The record is read again before the change; Current stands
	// in when that fails.
	Current *herdr.InstalledPluginInfo
	Target  Target
}

// Outcome is what a change did. Before and After are the plugin's states
// around it, nil when it was not installed.
type Outcome struct {
	Kind   ChangeKind
	ID     string
	Before *State
	After  *State
	// AfterUnknown is set when the plugin list could not be read afterwards.
	AfterUnknown bool
	// Err is why the change failed or is incomplete.
	Err error
	// Entry is the history entry recorded for the change, empty when none
	// was.
	Entry string
	// Log is the file with everything herdr printed, empty when none was
	// kept.
	Log string
}

// Changed reports whether the plugin is at another revision, or in or out
// of the list, after the change.
func (o Outcome) Changed() bool {
	switch {
	case o.AfterUnknown:
		return true
	case o.Before == nil || o.After == nil:
		return o.Before != o.After
	}
	return !o.Before.SameRevision(*o.After) || o.Before.Enabled != o.After.Enabled
}

// Summary says where the plugin stands after the change, such as
// "o.a: 1.0.0 at v1.0.0 (…), enabled -> 1.1.0 at v1.1.0 (…), enabled".
func (o Outcome) Summary() string {
	switch {
	case o.AfterUnknown:
		return o.ID + ": its state is unknown; the plugin list could not be read afterwards"
	case o.Before == nil && o.After == nil:
		return o.ID + " is not installed"
	case o.After == nil:
		return o.ID + " is no longer installed (was " + o.Before.String() + ")"
	case o.Before == nil:
		return o.ID + " is installed: " + o.After.String()
	case !o.Changed():
		return o.ID + " is unchanged: " + o.After.String()
	}
	return o.ID + ": " + o.Before.String() + " -> " + o.After.String()
}

// Error is nil for a change that succeeded, and otherwise its error followed
// by where the plugin stands.
func (o Outcome) Error() error {
	if o.Err == nil {
		return nil
	}
	return &OutcomeError{Outcome: o}
}

// OutcomeError is a failed change with the state it left the plugin in.
type OutcomeError struct{ Outcome Outcome }

func (e *OutcomeError) Error() string {
	return e.Outcome.Err.Error() + "\n" + e.Outcome.Summary()
}

func (e *OutcomeError) Unwrap() error { return e.Outcome.Err }

// ErrKeepDisabled is returned for a change that would leave a disabled
// plugin enabled: herdr registers every plugin it installs as enabled, and
// only a running server can disable it again.
var ErrKeepDisabled = errors.New("keeping the plugin disabled needs a running herdr server, since herdr enables every plugin it installs")

// afterTimeout bounds reading the plugin list after a change, which runs
// even when the change was cancelled.
const afterTimeout = 10 * time.Second

// Apply installs c.Target and leaves the plugin enabled or disabled as the
// target says. herdr's own output goes to out. The change is recorded in the
// history, with everything herdr printed.
//
// A change is refused before anything runs when the target's ref has moved
// since its preview, or when the plugin is to end up disabled and no server
// can disable it after the install. A failed herdr install leaves the plugin
// as it was; the outcome reads the record again to report what is installed
// either way.
func (m *Manager) Apply(ctx context.Context, c Change, out io.Writer) Outcome {
	if out == nil {
		out = io.Discard
	}
	o := Outcome{Kind: c.Kind, ID: c.ID}
	current, err := m.find(ctx, c.ID)
	if err != nil {
		current = c.Current
	}
	if current != nil {
		s := StateOf(*current)
		o.Before = &s
	}
	want := true
	switch {
	case c.Target.Enabled != nil:
		want = *c.Target.Enabled
	case o.Before != nil:
		want = o.Before.Enabled
	}

	entry := Entry{Kind: c.Kind, Plugin: c.ID, Target: c.Target.record(), Before: o.Before}
	logFile, logPath := m.openLog(&entry)
	if logFile != nil {
		defer func() { _ = logFile.Close() }()
		out = io.MultiWriter(out, logFile)
		o.Log = logPath
	}
	done := func() Outcome {
		entry.After, entry.AfterUnknown = o.After, o.AfterUnknown
		if o.Err != nil {
			entry.Error = o.Err.Error()
			if logFile != nil {
				fmt.Fprintf(logFile, "\n%s\n", o.Error())
			}
		}
		if err := m.History.add(&entry); err != nil {
			fmt.Fprintf(out, "warning: could not record the change in the history: %v\n", err)
		} else if m.History != nil && m.History.Dir != "" {
			o.Entry = entry.ID
		}
		return o
	}
	refuse := func(err error) Outcome {
		o.After, o.Err = o.Before, err
		return done()
	}

	if !want {
		if err := m.ping(ctx); err != nil {
			return refuse(fmt.Errorf("%w: %w", ErrKeepDisabled, err))
		}
	}
	if err := m.unmoved(ctx, c.Target.Source, c.Target.Ref, c.Target.Commit); err != nil {
		return refuse(err)
	}
	installErr := m.CLI.Install(ctx, c.Target.Source.String(), c.Target.Ref, out)

	// The record is read even when ctx was cancelled, to say where the
	// plugin stands.
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), afterTimeout)
	defer cancel()
	after, err := m.find(readCtx, c.ID)
	if err != nil {
		o.AfterUnknown = true
	} else if after != nil {
		s := StateOf(*after)
		o.After = &s
	}
	if installErr != nil {
		o.Err = installErr
		return done()
	}
	var errs []error
	switch {
	case o.AfterUnknown:
	case o.After == nil:
		errs = append(errs, fmt.Errorf("herdr reported success, but no plugin %s is installed; the manifest may declare another id", c.ID))
	case o.After.Commit != c.Target.Commit:
		errs = append(errs, fmt.Errorf("herdr installed %s at %.12s, not the previewed %.12s; review the plugin before using it",
			c.Target.Source, o.After.Commit, c.Target.Commit))
	}
	if o.After != nil && o.After.Enabled != want {
		if err := m.setEnabledWithin(readCtx, c.ID, want); err != nil {
			errs = append(errs, fmt.Errorf("herdr left %s %s, and it could not be %s again: %w",
				c.ID, enabledWord(o.After.Enabled), enabledWord(want), err))
		} else {
			o.After.Enabled = want
		}
	}
	o.Err = errors.Join(errs...)
	return done()
}

func (m *Manager) setEnabledWithin(ctx context.Context, id string, enabled bool) error {
	if !enabled && id == m.SelfID {
		return ErrSelf
	}
	return m.SetEnabled(ctx, id, enabled)
}

func enabledWord(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

// find reads the record of plugin id, nil when it is not installed.
func (m *Manager) find(ctx context.Context, id string) (*herdr.InstalledPluginInfo, error) {
	plugins, err := m.Installed(ctx)
	if err != nil {
		return nil, err
	}
	for i := range plugins {
		if plugins[i].PluginID == id {
			return &plugins[i], nil
		}
	}
	return nil, nil //nolint:nilnil // No record: the plugin is not installed.
}

// Find returns the record of plugin id, nil when it is not installed.
func (m *Manager) Find(ctx context.Context, id string) (*herdr.InstalledPluginInfo, error) {
	return m.find(ctx, id)
}

// ping reports whether a herdr server answers.
func (m *Manager) ping(ctx context.Context) error {
	api, err := m.api()
	if err != nil {
		return err
	}
	_, err = api.Ping(ctx)
	return serverErr(err)
}

// openLog creates the file the change's output is kept in, giving the entry
// its id. A log that cannot be created is left out.
func (m *Manager) openLog(e *Entry) (f *os.File, path string) {
	h := m.History
	if h == nil || h.Dir == "" {
		return nil, ""
	}
	e.Time = time.Now()
	e.ID = entryID(e.Time)
	if err := os.MkdirAll(h.logDir(), 0o700); err != nil {
		return nil, ""
	}
	path = filepath.Join(h.logDir(), e.ID+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // A new file named by the entry id in the history directory.
	if err != nil {
		return nil, ""
	}
	fmt.Fprintf(f, "%s %s %s at %s\n\n", e.Time.Format(time.DateTime), e.Kind, e.Plugin, targetLabel(e.Target))
	e.Log = path
	return f, path
}

func targetLabel(t *TargetRecord) string {
	if t == nil {
		return "-"
	}
	return t.Source + " @ " + RevisionLabel(t.Ref, t.Commit)
}

// Uninstall removes a plugin: a GitHub install loses its managed checkout, a
// linked plugin is only unregistered and its directory is left alone. The
// change is recorded in the history.
func (m *Manager) Uninstall(ctx context.Context, id string, out io.Writer) error {
	if id == m.SelfID {
		return ErrSelf
	}
	o := Outcome{Kind: KindUninstall, ID: id}
	if current, err := m.find(ctx, id); err == nil && current != nil {
		s := StateOf(*current)
		o.Before = &s
	}
	err := m.CLI.Uninstall(ctx, id, out)
	entry := Entry{Kind: KindUninstall, Plugin: id, Before: o.Before}
	if err != nil {
		entry.After, entry.Error = o.Before, err.Error()
	}
	if herr := m.History.add(&entry); herr != nil && out != nil {
		fmt.Fprintf(out, "warning: could not record the change in the history: %v\n", herr)
	}
	return err
}

// Undo is the change that takes a plugin back to where it was before the
// last change recorded for it.
type Undo struct {
	// Entry is the change being undone.
	Entry Entry
	// Current is the plugin's record now, nil when it is not installed.
	Current *herdr.InstalledPluginInfo
	// Remove is set when the change installed the plugin, so undoing it
	// uninstalls it; Target is then unused.
	Remove bool
	Target Target
	// Pinned is set when the earlier revision can only be installed as a
	// commit pin: its ref has moved on since, so the plugin will no longer
	// follow that ref until it is unpinned.
	Pinned bool
}

// ErrNothingToUndo is returned when no recorded change can be undone.
var ErrNothingToUndo = errors.New("nothing to roll back")

// PlanRollback finds the last change recorded for plugin id and the change
// that undoes it. It is refused when the plugin has changed since in a way
// the history does not know of, or when the earlier state was a local link.
func (m *Manager) PlanRollback(ctx context.Context, id string) (Undo, error) {
	e, ok, err := m.History.LastChange(id)
	if err != nil {
		return Undo{}, err
	}
	if !ok {
		return Undo{}, fmt.Errorf("%w: no change to %s is recorded", ErrNothingToUndo, id)
	}
	current, err := m.find(ctx, id)
	if err != nil {
		return Undo{}, err
	}
	u := Undo{Entry: e, Current: current}
	when := e.Time.Local().Format(time.DateTime)
	switch {
	case e.AfterUnknown:
		return Undo{}, fmt.Errorf("%w: the state %s was left in by the %s at %s is unknown", ErrNothingToUndo, id, e.Kind, when)
	case (current == nil) != (e.After == nil) || current != nil && !StateOf(*current).SameRevision(*e.After):
		return Undo{}, fmt.Errorf("%w: %s changed after the %s at %s, outside this manager", ErrNothingToUndo, id, e.Kind, when)
	case e.Before == nil:
		u.Remove = true
		return u, nil
	case e.Before.Source == "":
		return Undo{}, fmt.Errorf("%s was linked from %s before; link it again with herdr plugin link", id, e.Before.Root)
	}
	src, err := source.Parse(e.Before.Source)
	if err != nil {
		return Undo{}, err
	}
	enabled := e.Before.Enabled
	u.Target = Target{Source: src, Ref: e.Before.Ref, Commit: e.Before.Commit, Enabled: &enabled}
	if !updates.IsCommit(e.Before.Ref) {
		now, err := m.resolve(ctx, src, e.Before.Ref)
		if err != nil || now != e.Before.Commit {
			u.Target.Ref, u.Pinned = e.Before.Commit, true
		}
	}
	return u, nil
}

// Describe says what undoing does, on one line.
func (u Undo) Describe() string {
	when := u.Entry.Time.Local().Format(time.DateTime)
	if u.Remove {
		return fmt.Sprintf("undo the %s of %s at %s: uninstall it", u.Entry.Kind, u.Entry.Plugin, when)
	}
	s := fmt.Sprintf("undo the %s of %s at %s: back to %s", u.Entry.Kind, u.Entry.Plugin, when, u.Entry.Before)
	if u.Pinned {
		s += fmt.Sprintf("; %s has moved on, so %s will be pinned to that commit", RevisionLabel(u.Entry.Before.Ref, ""), u.Entry.Plugin)
	}
	return s
}

// Rollback applies u.
func (m *Manager) Rollback(ctx context.Context, u Undo, out io.Writer) Outcome {
	if !u.Remove {
		return m.Apply(ctx, Change{Kind: KindRollback, ID: u.Entry.Plugin, Current: u.Current, Target: u.Target}, out)
	}
	o := Outcome{Kind: KindRollback, ID: u.Entry.Plugin, After: u.Entry.After, Before: u.Entry.After}
	if err := m.Uninstall(ctx, u.Entry.Plugin, out); err != nil {
		o.Err = err
		return o
	}
	o.After = nil
	return o
}

// HistoryEntries lists the recorded changes, the oldest first.
func (m *Manager) HistoryEntries() ([]Entry, error) { return m.History.List() }

// ReadLog returns the output kept for an entry.
func ReadLog(e Entry) (string, error) {
	if e.Log == "" {
		return "", errors.New("no output was kept for this change")
	}
	data, err := os.ReadFile(e.Log)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\n"), nil
}
