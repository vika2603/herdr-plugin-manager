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
	// History records the changes made to installed plugins; nil keeps none.
	History *History
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

// Check compares one installed plugin with its remote. For a plugin in a
// subdirectory, new commits count only when they change that directory: the
// rest of the repository is often another project, or other plugins.
func (m *Manager) Check(ctx context.Context, p herdr.InstalledPluginInfo) (updates.Result, error) {
	res, err := updates.Check(ctx, m.Git, p)
	dirs, ok := m.Git.(updates.DirComparer)
	if err != nil || !ok || res.Kind != updates.Available || res.Source.Subdir == "" ||
		!updates.IsCommit(res.CurrentCommit) || !updates.IsCommit(res.TargetCommit) {
		return res, err
	}
	// When the comparison fails, the update stands.
	if same, err := dirs.SameDir(ctx, res.Source.CloneURL(), res.Source.Subdir, res.CurrentCommit, res.TargetCommit); err == nil && same {
		res.Kind = updates.UpToDate
	}
	return res, nil
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

// Releases lists the published releases of src's repository, the newest
// first.
func (m *Manager) Releases(ctx context.Context, src source.GitHub) ([]market.Release, error) {
	return m.Market.Releases(ctx, src)
}

// Index loads the marketplace index.
func (m *Manager) Index(ctx context.Context, refresh bool) (*market.Index, market.Status, error) {
	return m.Market.Load(ctx, refresh)
}

// Preview reads the manifest src would install at ref and checks it against
// this machine. An empty ref installs the latest release of a plugin at the
// root of its repository, and the default branch otherwise: the tags of a
// repository with a plugin in a subdirectory usually version something else.
// installed is the current plugin list, used to report what an install would
// replace.
func (m *Manager) Preview(ctx context.Context, src source.GitHub, ref, hint, herdrVersion string, installed []herdr.InstalledPluginInfo) (*Preview, error) {
	t, mf, warnings, err := m.manifestAt(ctx, src, ref, hint)
	if err != nil {
		return nil, err
	}
	p := &Preview{
		Source: src, Ref: t.ref, Commit: t.commit, Releases: t.releases, DefaultBranch: t.branch,
		Manifest: mf, Warnings: warnings, Platform: m.Platform,
	}
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

// target is what an install checks out.
type target struct {
	ref, commit string
	// releases and branch are the release tags and the default branch the
	// remote offers, when it was listed.
	releases []string
	branch   string
}

// pick resolves ref, choosing one for an empty ref as Preview describes.
func (m *Manager) pick(ctx context.Context, src source.GitHub, ref string) (target, error) {
	if updates.IsCommit(ref) {
		return target{ref: ref, commit: ref}, nil
	}
	refs, err := m.Git.List(ctx, src.CloneURL())
	if err != nil {
		return target{}, err
	}
	t := target{ref: ref, releases: refs.Releases(), branch: refs.HeadBranch}
	if ref == "" && src.Subdir == "" {
		t.ref = refs.LatestRelease()
	}
	commit, ok := refs.Resolve(t.ref)
	if !ok {
		return target{}, fmt.Errorf("%s has no ref %q", src.Repository(), ref)
	}
	t.commit = commit
	return t, nil
}

// manifestAt picks what to install and reads the manifest at its commit.
// hint is the commit that is expected, such as the marketplace index's
// record of the default branch: its manifest is downloaded while the remote
// is listed, and used when the guess is right.
func (m *Manager) manifestAt(ctx context.Context, src source.GitHub, ref, hint string) (t target, mf *manifest.Manifest, warnings []string, err error) {
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
	if t, err = m.pick(ctx, src, ref); err != nil {
		return target{}, nil, nil, err
	}
	if guess != nil && t.commit == hint {
		f := <-guess
		return t, f.mf, f.warnings, f.err
	}
	mf, warnings, err = m.Market.Manifest(ctx, src, t.commit)
	return t, mf, warnings, err
}
