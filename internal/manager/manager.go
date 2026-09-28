// Package manager combines the sources the plugin manager works from: the
// herdr socket API, the herdr command, the marketplace index and the plugins'
// GitHub remotes. Both the popup and the command line drive it.
//
// herdr's socket API has no install or uninstall method, so those go through
// the herdr command, which also works while no server is running. Enabling,
// disabling and reading logs exist only in the API: the herdr command
// forwards them to the server as well, so they need a running server either
// way.
package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/vika2603/herdr-client/herdr"
	"github.com/vika2603/herdr-client/plugin/manifest"

	"github.com/vika2603/herdr-plugin-manager/internal/compat"
	"github.com/vika2603/herdr-plugin-manager/internal/herdrcli"
	"github.com/vika2603/herdr-plugin-manager/internal/market"
	"github.com/vika2603/herdr-plugin-manager/internal/safe"
	"github.com/vika2603/herdr-plugin-manager/internal/source"
	"github.com/vika2603/herdr-plugin-manager/internal/updates"
)

// ErrServerNotRunning is returned for operations only a running herdr server
// can perform.
var ErrServerNotRunning = errors.New("herdr server is not running")

// ErrSelf protects the manager from removing or disabling itself from its own
// popup, which would end the process doing the work.
var ErrSelf = errors.New("the plugin manager cannot remove or disable itself; use the herdr command")

// checkConcurrency bounds parallel `git ls-remote` calls.
const checkConcurrency = 8

// Manager performs plugin operations.
type Manager struct {
	// API is the herdr socket client, nil when none could be configured.
	API    *herdr.Client
	CLI    herdrcli.Runner
	Market *market.Client
	Git    updates.Lister
	// SelfID is the manager's own plugin id when it runs as a plugin.
	SelfID string
	// Platform is the manifest platform name of this machine.
	Platform string
}

// IsServerDown reports whether err means no herdr server answered.
func IsServerDown(err error) bool {
	if errors.Is(err, ErrServerNotRunning) {
		return true
	}
	opErr, ok := errors.AsType[*herdr.OpError](err)
	return ok && opErr.Op == herdr.OpDial
}

func (m *Manager) api() (*herdr.Client, error) {
	if m.API == nil {
		return nil, ErrServerNotRunning
	}
	return m.API, nil
}

func serverErr(err error) error {
	if IsServerDown(err) {
		return fmt.Errorf("%w: %w", ErrServerNotRunning, err)
	}
	return err
}

// Installed lists installed and linked plugins sorted by id. It asks the
// server, and reads the registry through the herdr command when no server is
// running.
func (m *Manager) Installed(ctx context.Context) ([]herdr.InstalledPluginInfo, error) {
	var plugins []herdr.InstalledPluginInfo
	if api, err := m.api(); err == nil {
		res, err := api.PluginList(ctx, herdr.PluginListParams{})
		if err != nil && !IsServerDown(err) {
			return nil, err
		}
		if err == nil {
			plugins = res.Plugins
		}
	}
	if plugins == nil {
		var err error
		if plugins, err = m.CLI.List(ctx); err != nil {
			return nil, err
		}
	}
	slices.SortFunc(plugins, func(a, b herdr.InstalledPluginInfo) int {
		return strings.Compare(a.PluginID, b.PluginID)
	})
	for i := range plugins {
		sanitizeInstalled(&plugins[i])
	}
	return plugins, nil
}

// sanitizeInstalled makes the manifest text herdr reports printable. Ids and
// paths are herdr's and are used for further calls, so they stay as they are;
// commands are escaped when they are drawn, by Command.
func sanitizeInstalled(p *herdr.InstalledPluginInfo) {
	p.Name, p.Version = safe.Line(p.Name), safe.Line(p.Version)
	if d, ok := p.Description.Get(); ok {
		p.Description = herdr.Some(safe.Line(d))
	}
	if v, ok := p.MinHerdrVersion.Get(); ok {
		p.MinHerdrVersion = herdr.Some(safe.Line(v))
	}
	if w, ok := p.Warnings.Get(); ok {
		for i := range w {
			w[i] = safe.Line(w[i])
		}
	}
	if actions, ok := p.Actions.Get(); ok {
		for i := range actions {
			actions[i].Title = safe.Line(actions[i].Title)
		}
	}
	if panes, ok := p.Panes.Get(); ok {
		for i := range panes {
			panes[i].Title = safe.Line(panes[i].Title)
		}
	}
}

// HerdrVersion is the version of the running server, or of the herdr
// command when no server answers. It is empty when neither is available.
func (m *Manager) HerdrVersion(ctx context.Context) string {
	if api, err := m.api(); err == nil {
		if pong, err := api.Ping(ctx); err == nil {
			return pong.Version
		}
	}
	v, _ := m.CLI.Version(ctx)
	return v
}

// SetEnabled enables or disables a plugin.
func (m *Manager) SetEnabled(ctx context.Context, id string, enabled bool) error {
	if !enabled && id == m.SelfID {
		return ErrSelf
	}
	api, err := m.api()
	if err != nil {
		return err
	}
	params := herdr.PluginSetEnabledParams{PluginID: id}
	if enabled {
		_, err = api.PluginEnable(ctx, params)
	} else {
		_, err = api.PluginDisable(ctx, params)
	}
	return serverErr(err)
}

// Logs returns the most recent command logs herdr kept for a plugin, newest
// last as herdr reports them.
func (m *Manager) Logs(ctx context.Context, id string, limit int) ([]herdr.PluginCommandLogInfo, error) {
	api, err := m.api()
	if err != nil {
		return nil, err
	}
	params := herdr.PluginLogListParams{PluginID: herdr.Some(id)}
	if limit > 0 {
		params.Limit = herdr.Some(uint64(limit))
	}
	res, err := api.PluginLogList(ctx, params)
	if err != nil {
		return nil, serverErr(err)
	}
	// Logs hold whatever the plugin's commands printed.
	for i := range res.Logs {
		l := &res.Logs[i]
		for _, f := range []*herdr.Optional[string]{&l.Stdout, &l.Stderr} {
			if text, ok := f.Get(); ok {
				*f = herdr.Some(safe.Text(text))
			}
		}
		if e, ok := l.Error.Get(); ok {
			l.Error = herdr.Some(safe.Line(e))
		}
	}
	return res.Logs, nil
}

// ErrMoved is returned when a ref no longer points at the commit that was
// previewed, so the build commands about to run were never shown.
var ErrMoved = errors.New("the source changed since its preview; review it again")

// Install installs src at ref (empty for the default branch), writing herdr's
// output to out. commit is what the preview read; the install is refused
// when ref has moved since, and checked against what herdr installed.
func (m *Manager) Install(ctx context.Context, src source.GitHub, ref, commit string, out io.Writer) error {
	if err := m.unmoved(ctx, src, ref, commit); err != nil {
		return err
	}
	if err := m.CLI.Install(ctx, src.String(), ref, out); err != nil {
		return err
	}
	return m.installedAt(ctx, src, commit)
}

// resolve returns the commit ref of src points at now.
func (m *Manager) resolve(ctx context.Context, src source.GitHub, ref string) (string, error) {
	if updates.IsCommit(ref) {
		return ref, nil
	}
	refs, err := m.Git.List(ctx, src.CloneURL())
	if err != nil {
		return "", err
	}
	commit, ok := refs.Resolve(ref)
	if !ok {
		return "", fmt.Errorf("%s has no ref %q", src.Repository(), ref)
	}
	return commit, nil
}

// unmoved checks that ref still points at commit.
func (m *Manager) unmoved(ctx context.Context, src source.GitHub, ref, commit string) error {
	now, err := m.resolve(ctx, src, ref)
	if err != nil {
		return err
	}
	if now != commit {
		return fmt.Errorf("%w (%s was %.12s, is now %.12s)", ErrMoved, src, commit, now)
	}
	return nil
}

// installedAt reports a plugin herdr installed from src at another commit
// than commit. A moment remains between the check before install and herdr's
// own fetch; this catches a push in it, after the fact. When the plugin
// list cannot be read the check is skipped.
func (m *Manager) installedAt(ctx context.Context, src source.GitHub, commit string) error {
	plugins, err := m.Installed(ctx)
	if err != nil {
		return nil //nolint:nilerr // The install itself succeeded; only the after-check is skipped.
	}
	for _, p := range plugins {
		got, ok := source.FromInstalled(p)
		if !ok || got != src {
			continue
		}
		if installed := p.Source.ValueOrZero().ResolvedCommit.ValueOrZero(); installed != commit {
			return fmt.Errorf("herdr installed %s at %.12s, not the previewed %.12s; review the plugin before using it", src, installed, commit)
		}
	}
	return nil
}

// Uninstall removes a plugin: a GitHub install loses its managed checkout, a
// linked plugin is only unregistered and its directory is left alone.
func (m *Manager) Uninstall(ctx context.Context, id string, out io.Writer) error {
	if id == m.SelfID {
		return ErrSelf
	}
	return m.CLI.Uninstall(ctx, id, out)
}

// Update reinstalls a plugin at the target a check found. A reinstall
// registers the plugin as enabled, so a plugin that was disabled is disabled
// again afterwards.
func (m *Manager) Update(ctx context.Context, p herdr.InstalledPluginInfo, res updates.Result, out io.Writer) error {
	if res.Kind != updates.Available {
		return fmt.Errorf("%s has no update available", p.PluginID)
	}
	if err := m.unmoved(ctx, res.Source, res.TargetRef, res.TargetCommit); err != nil {
		return err
	}
	if err := m.CLI.Install(ctx, res.Source.String(), res.TargetRef, out); err != nil {
		return err
	}
	if err := m.installedAt(ctx, res.Source, res.TargetCommit); err != nil {
		return err
	}
	if p.Enabled {
		return nil
	}
	if err := m.SetEnabled(ctx, p.PluginID, false); err != nil {
		return fmt.Errorf("updated %s, but it was re-enabled and could not be disabled again: %w", p.PluginID, err)
	}
	return nil
}

// Check looks for an update of one plugin.
func (m *Manager) Check(ctx context.Context, p herdr.InstalledPluginInfo) (updates.Result, error) {
	return updates.Check(ctx, m.Git, p)
}

// Checked is the outcome of checking one plugin in CheckAll.
type Checked struct {
	Plugin herdr.InstalledPluginInfo
	Result updates.Result
	Err    error
}

// CheckAll checks every plugin concurrently, keeping the input order.
func (m *Manager) CheckAll(ctx context.Context, plugins []herdr.InstalledPluginInfo) []Checked {
	out := make([]Checked, len(plugins))
	sem := make(chan struct{}, checkConcurrency)
	var wg sync.WaitGroup
	for i, p := range plugins {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			res, err := m.Check(ctx, p)
			out[i] = Checked{Plugin: p, Result: res, Err: err}
		})
	}
	wg.Wait()
	return out
}

// Index loads the marketplace index.
func (m *Manager) Index(ctx context.Context, refresh bool) (*market.Index, market.Status, error) {
	return m.Market.Load(ctx, refresh)
}

// Preview reads the manifest src would install at ref and checks it against
// this machine. installed is the current plugin list, used to report what an
// install would replace.
func (m *Manager) Preview(ctx context.Context, src source.GitHub, ref, hint, herdrVersion string, installed []herdr.InstalledPluginInfo) (*Preview, error) {
	commit, mf, warnings, err := m.manifestAt(ctx, src, ref, hint)
	if err != nil {
		return nil, err
	}
	p := &Preview{Source: src, Ref: ref, Commit: commit, Manifest: mf, Warnings: warnings, Platform: m.Platform}
	platforms := make([]string, len(mf.Platforms))
	for i, pl := range mf.Platforms {
		platforms[i] = string(pl)
	}
	p.Problems = compat.Problems(platforms, mf.MinHerdrVersion, herdrVersion, m.Platform)
	for i := range installed {
		if installed[i].PluginID == mf.ID {
			existing := installed[i]
			p.Existing = &existing
			if _, ok := source.FromInstalled(existing); !ok {
				p.Problems = append(p.Problems, "a local plugin with this id is linked; unlink it before installing from GitHub")
			}
		}
	}
	return p, nil
}

// manifestAt resolves ref and reads the manifest at the commit it points at.
// hint is the commit ref is expected at, such as the marketplace index's
// record of the default branch: its manifest is downloaded while ref
// resolves, and used when the guess is right.
func (m *Manager) manifestAt(ctx context.Context, src source.GitHub, ref, hint string) (commit string, mf *manifest.Manifest, warnings []string, err error) {
	type fetched struct {
		mf       *manifest.Manifest
		warnings []string
		err      error
	}
	var guess chan fetched
	if updates.IsCommit(hint) && !updates.IsCommit(ref) {
		guessCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		guess = make(chan fetched, 1)
		go func() {
			mf, warnings, err := m.Market.Manifest(guessCtx, src, hint)
			guess <- fetched{mf, warnings, err}
		}()
	}
	if commit, err = m.resolve(ctx, src, ref); err != nil {
		return "", nil, nil, err
	}
	if guess != nil && commit == hint {
		f := <-guess
		return commit, f.mf, f.warnings, f.err
	}
	mf, warnings, err = m.Market.Manifest(ctx, src, commit)
	return commit, mf, warnings, err
}
