// Package ui is the interactive manager. The same model runs in a herdr popup
// and in a plain terminal; only the screen mode differs.
package ui

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-plugin-manager/internal/compat"
	"github.com/vika2603/herdr-plugin-manager/internal/config"
	"github.com/vika2603/herdr-plugin-manager/internal/manager"
	"github.com/vika2603/herdr-plugin-manager/internal/market"
	"github.com/vika2603/herdr-plugin-manager/internal/safe"
	"github.com/vika2603/herdr-plugin-manager/internal/source"
	"github.com/vika2603/herdr-plugin-manager/internal/updates"
)

// Backend is what the model needs from the manager. *manager.Manager
// implements it; tests substitute a fake.
type Backend interface {
	Installed(ctx context.Context) ([]herdr.InstalledPluginInfo, error)
	HerdrVersion(ctx context.Context) string
	SetEnabled(ctx context.Context, id string, enabled bool) error
	Logs(ctx context.Context, id string, limit int) ([]herdr.PluginCommandLogInfo, error)
	Apply(ctx context.Context, c manager.Change, out io.Writer) manager.Outcome
	Uninstall(ctx context.Context, id string, out io.Writer) error
	PlanRollback(ctx context.Context, id string) (manager.Undo, error)
	Versions(ctx context.Context, src source.GitHub) (manager.Versions, error)
	Explain(ctx context.Context, p herdr.InstalledPluginInfo, preview *manager.Preview) manager.Explanation
	Review(ctx context.Context, ch manager.Checked, herdrVersion string) manager.Review
	PreviewAt(ctx context.Context, src source.GitHub, ref, commit, herdrVersion string, installed []herdr.InstalledPluginInfo) (*manager.Preview, error)
	Rollback(ctx context.Context, u manager.Undo, out io.Writer) manager.Outcome
	CheckAll(ctx context.Context, plugins []herdr.InstalledPluginInfo) []manager.Checked
	Index(ctx context.Context, refresh bool) (*market.Index, market.Status, error)
	Preview(ctx context.Context, src source.GitHub, ref, hint, herdrVersion string, installed []herdr.InstalledPluginInfo) (*manager.Preview, error)
	RemoteReadme(ctx context.Context, src source.GitHub, commit string) (*manager.Readme, error)
	Releases(ctx context.Context, src source.GitHub) ([]market.Release, error)
	InstalledReadme(p herdr.InstalledPluginInfo) (*manager.Readme, error)
	OpenURL(ctx context.Context, url string) error
	HistoryEntries() ([]manager.Entry, error)
	Usage(ctx context.Context, p herdr.InstalledPluginInfo) manager.Usage
}

// Options adjust the program to where it runs.
type Options struct {
	// AltScreen draws on the alternate screen, which a plain terminal wants
	// and a herdr popup does not: the popup is a pane of its own that herdr
	// destroys on close, and staying on the main screen keeps it readable to
	// pane.read.
	AltScreen bool
	// SelfID is the manager's own plugin id when it runs as a plugin. The
	// list refuses to remove or disable it before asking for confirmation.
	SelfID string
	// Config is the config file's settings: keys and theme.
	Config config.Config
	// ConfigErr is a problem reading the config file, shown on start.
	ConfigErr error
}

// Run runs the manager until the user quits or ctx ends. A context ended by
// herdr closing the popup is a normal exit.
//
// Quitting while an install, update or uninstall runs cancels it and waits
// for it to stop, so the herdr command it started is not left running.
func Run(ctx context.Context, b Backend, opts Options) error {
	ctx, cancel := context.WithCancel(ctx)
	m := newModel(ctx, b, opts)
	program := tea.NewProgram(m, tea.WithContext(ctx))
	_, err := program.Run()
	m.shutdown(cancel)
	if ctx.Err() != nil {
		return nil //nolint:nilerr // herdr closing the popup ends ctx, which is a normal exit.
	}
	return err
}

type tab int

const (
	tabInstalled tab = iota
	tabBrowse
)

type screen int

const (
	screenList screen = iota
	screenDetail
	screenOutput
	// screenReview is the review of several updates before they run.
	screenReview
	// screenHistory lists the changes recorded in the history.
	screenHistory
)

// logLimit is how many command logs the detail view asks for.
const logLimit = 10

type model struct {
	ctx context.Context
	// ops counts running installs, updates and uninstalls, for shutdown.
	ops  *sync.WaitGroup
	b    Backend
	opts Options
	// now is the clock, for how long ago a repository was pushed, and
	// platform this machine's manifest platform name.
	now      func() time.Time
	platform string

	width, height int
	tab           tab
	screen        screen

	herdrVersion string

	installed    []herdr.InstalledPluginInfo
	installedErr error
	loaded       bool
	checks       map[string]manager.Checked
	checking     bool
	checkGen     int

	entries      []market.Entry
	indexStatus  market.Status
	indexErr     error
	indexLoading bool
	order        market.Order
	// entriesVersion changes whenever entries is replaced or reordered.
	entriesVersion int
	search         searchCache

	filters [2]textinput.Model
	cursor  [2]int
	offset  [2]int

	detail  *detail
	confirm *confirm
	// review is open while the available updates are reviewed together.
	review *batchReview
	// readmes keeps the READMEs read in this session, by source and ref.
	// palettes are the colours of the two backgrounds, and themeMode the
	// config's choice between them; "" or auto follows the terminal.
	palettes  [2]palette
	themeMode string
	// keys is what each key does, and helpKeys how the help bar shows them.
	keys     keymap
	helpKeys helpKeys
	readmes  map[string]cachedReadme
	// releases are the release notes read in the session, by repository.
	releases map[string]cachedReleases

	theme    theme
	help     help.Model
	showHelp bool

	spinner spinner.Model
	ticking bool
	// busy describes the running install, update or uninstall, and live
	// is its output so far; only one runs at a time.
	busy string
	live *liveOp
	// toggling counts the enable and disable requests not yet answered;
	// no operation starts while one is.
	toggling int
	// output is the last operation's; page is what the output screen shows
	// when no operation runs: output, or a change from the history.
	output output
	page   *output
	// outputFollow keeps the output screen at its end as lines arrive;
	// outputMax is the last offset it drew.
	outputOffset int
	outputFollow bool
	outputMax    int
	outputLines  outputCache
	// history is open while the recorded changes are listed.
	history *historyView
	// showAfterLoad is the plugin whose details open when the list is next
	// loaded.
	showAfterLoad string

	status    string
	statusErr bool
	// statusGen counts the statuses set, so that a status is only cleared
	// by the timer started when it was set.
	statusGen int
}

// detail is the scrollable view of one plugin: an installed one, or the
// preview of an install or update.
type detail struct {
	// crumb names the tab the detail was opened from, title the plugin.
	crumb   string
	title   string
	plugin  *herdr.InstalledPluginInfo
	logs    []herdr.PluginCommandLogInfo
	logsErr error
	// usage says how an installed plugin is used, once it is read.
	usage *manager.Usage

	loading bool
	preview *manager.Preview
	err     error
	// install or change is what the preview's main key does.
	install *installTarget
	change  *pendingChange
	// entry is the marketplace listing an install was opened from.
	entry *market.Entry
	// versions is open while the user picks another version to preview.
	versions *versionPicker
	// review is the batch review the detail was opened from.
	review *batchReview
	// explain describes a change's preview once it is read, and notes are
	// its release notes rendered at notesWidth.
	explain    *manager.Explanation
	notes      []string
	notesWidth int

	view   view
	readme readme
	// offsets are the scroll positions of the two views.
	offsets [2]int
}

type searchCache struct {
	valid   bool
	query   string
	order   market.Order
	version int
	result  []market.Entry
}

type installTarget struct {
	src source.GitHub
	ref string
	// commit is what the preview read at ref.
	commit string
}

// pendingChange is a change to an installed plugin, shown as a preview of
// the manifest it installs before it is applied.
type pendingChange struct {
	kind   manager.ChangeKind
	plugin herdr.InstalledPluginInfo
	target manager.Target
	// note says what the change does beyond the manifest shown.
	note string
	// fromPreview is set for a change whose target is what its preview
	// resolved; the others preview the commit their target already names.
	fromPreview bool
	// undo is the rollback a rollback applies.
	undo *manager.Undo
}

// changeVerbs are how a change is named: the key's description, the
// progress title and the preview's title.
var changeVerbs = map[manager.ChangeKind][3]string{
	manager.KindUpdate:    {"update", "Updating", "Update"},
	manager.KindRollback:  {"roll back", "Rolling back", "Roll back"},
	manager.KindSwitch:    {"switch", "Switching", "Switch"},
	manager.KindPin:       {"pin", "Pinning", "Pin"},
	manager.KindUnpin:     {"unpin", "Unpinning", "Unpin"},
	manager.KindReinstall: {"reinstall", "Reinstalling", "Reinstall"},
}

type confirm struct {
	prompt string
	run    func() tea.Cmd
}

type output struct {
	title     string
	text      string
	err       error
	cancelled bool
	// retry starts the part that did not succeed again, nil when there is
	// none; back is the screen the output returns to.
	retry func() tea.Cmd
	back  screen
}

func newModel(ctx context.Context, b Backend, opts Options) *model {
	m := &model{
		ctx:      ctx,
		ops:      &sync.WaitGroup{},
		b:        b,
		opts:     opts,
		now:      time.Now,
		platform: compat.Platform(),
		checks:   map[string]manager.Checked{},
		readmes:  map[string]cachedReadme{}, releases: map[string]cachedReleases{},
		help:    help.New(),
		spinner: spinner.New(spinner.WithSpinner(spinner.MiniDot)),
	}
	for i := range m.filters {
		f := textinput.New()
		f.Placeholder = "filter installed plugins"
		m.filters[i] = f
	}
	m.filters[tabBrowse].Placeholder = "search the marketplace"
	var err error
	if m.keys, err = newKeymap(opts.Config.Keys); err != nil {
		m.setStatus(err.Error()+"; using the default keys", true)
	}
	if m.palettes, err = palettes(opts.Config.Theme); err != nil {
		m.setStatus(err.Error()+"; using the default colours", true)
	}
	mode := opts.Config.Theme.Mode
	if !validMode(mode) {
		m.setStatus(fmt.Sprintf("config: theme mode %q is not auto, dark or light", mode), true)
		mode = ""
	}
	m.themeMode = mode
	if opts.ConfigErr != nil {
		m.setStatus(opts.ConfigErr.Error(), true)
	}
	m.helpKeys = newHelpKeys(m.keys)
	for i := range m.filters {
		m.filters[i].Prompt = cmp.Or(m.keys.name(actSearch), "›") + " "
	}
	// Dark until the terminal reports its background, which a herdr popup
	// may never do, unless the config fixes it.
	m.applyTheme(themeFor(m.themeMode != "light", m.palettes))
	return m
}

func (m *model) applyTheme(t theme) {
	m.theme = t
	m.help.Styles = t.help
	m.spinner.Style = t.accent
	for i := range m.filters {
		m.filters[i].SetStyles(t.input)
	}
}

// shutdownWait bounds how long shutdown waits. It is longer than herdrcli's
// wait delay, after which the herdr command is killed; it also covers an
// operation the program quit before running, which never finishes.
const shutdownWait = 15 * time.Second

// shutdown cancels the model's context and waits for running operations.
// herdrcli interrupts the herdr command on cancellation.
func (m *model) shutdown(cancel context.CancelFunc) {
	cancel()
	done := make(chan struct{})
	go func() {
		m.ops.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(shutdownWait):
	}
}

// Messages carrying the results of backend calls.
type (
	installedMsg struct {
		plugins []herdr.InstalledPluginInfo
		err     error
	}
	versionMsg string
	indexMsg   struct {
		entries []market.Entry
		status  market.Status
		err     error
	}
	checksMsg struct {
		// gen identifies the check, so that one started earlier cannot
		// replace the results of a later one.
		gen     int
		results []manager.Checked
	}
	logsMsg struct {
		id   string
		logs []herdr.PluginCommandLogInfo
		err  error
	}
	previewMsg struct {
		d       *detail
		preview *manager.Preview
		err     error
	}
	enabledMsg struct {
		id      string
		enabled bool
		err     error
	}
	usageMsg struct {
		d     *detail
		usage manager.Usage
	}
	explainMsg struct {
		d       *detail
		explain manager.Explanation
	}
	rollbackMsg struct {
		plugin herdr.InstalledPluginInfo
		undo   manager.Undo
		err    error
	}
)

func (m *model) Init() tea.Cmd {
	m.indexLoading, m.ticking = true, true
	return tea.Batch(m.loadInstalled(), m.loadVersion(), m.loadIndex(false), m.spinner.Tick, tea.RequestBackgroundColor)
}

func (m *model) loadInstalled() tea.Cmd {
	return func() tea.Msg {
		plugins, err := m.b.Installed(m.ctx)
		return installedMsg{plugins: plugins, err: err}
	}
}

func (m *model) loadVersion() tea.Cmd {
	return func() tea.Msg { return versionMsg(m.b.HerdrVersion(m.ctx)) }
}

func (m *model) loadIndex(refresh bool) tea.Cmd {
	return func() tea.Msg {
		ix, st, err := m.b.Index(m.ctx, refresh)
		msg := indexMsg{status: st, err: err}
		if ix != nil {
			msg.entries = ix.Entries()
		}
		return msg
	}
}

func (m *model) checkUpdates() tea.Cmd {
	plugins := m.installed
	m.checking = true
	m.checkGen++
	gen := m.checkGen
	return func() tea.Msg { return checksMsg{gen: gen, results: m.b.CheckAll(m.ctx, plugins)} }
}

func (m *model) loadUsage(d *detail, p herdr.InstalledPluginInfo) tea.Cmd {
	return func() tea.Msg { return usageMsg{d: d, usage: m.b.Usage(m.ctx, p)} }
}

func (m *model) loadLogs(id string) tea.Cmd {
	return func() tea.Msg {
		logs, err := m.b.Logs(m.ctx, id, logLimit)
		return logsMsg{id: id, logs: logs, err: err}
	}
}

// loadExplain describes what a change's preview changes, reading the
// release notes or commits it brings in.
func (m *model) loadExplain(d *detail) tea.Cmd {
	if d.change == nil || d.preview == nil {
		return nil
	}
	p, preview := d.change.plugin, d.preview
	return func() tea.Msg {
		return explainMsg{d: d, explain: m.b.Explain(m.ctx, p, preview)}
	}
}

// loadPreview reads the manifest an install of src at ref would bring in. A
// change that names its commit previews that commit.
func (m *model) loadPreview(d *detail, src source.GitHub, ref, hint string) tea.Cmd {
	version, installed := m.herdrVersion, m.installed
	c := d.change
	return func() tea.Msg {
		var (
			p   *manager.Preview
			err error
		)
		if c != nil && !c.fromPreview {
			p, err = m.b.PreviewAt(m.ctx, src, c.target.Ref, c.target.Commit, version, installed)
		} else {
			p, err = m.b.Preview(m.ctx, src, ref, hint, version, installed)
		}
		if err == nil && c != nil {
			p.RequireID(c.plugin.PluginID)
		}
		return previewMsg{d: d, preview: p, err: err}
	}
}

func (m *model) setEnabled(id string, enabled bool) tea.Cmd {
	return func() tea.Msg {
		return enabledMsg{id: id, enabled: enabled, err: m.b.SetEnabled(m.ctx, id, enabled)}
	}
}

// current is the latest record of p: whether a plugin is enabled may have
// changed since its update was checked, and Update keeps that state.
func (m *model) current(p herdr.InstalledPluginInfo) herdr.InstalledPluginInfo {
	for _, q := range m.installed {
		if q.PluginID == p.PluginID {
			return q
		}
	}
	return p
}

// updateChange is the change that applies an update a check found.
func updateChange(ch manager.Checked) pendingChange {
	r := ch.Result
	return pendingChange{
		kind: manager.KindUpdate, plugin: ch.Plugin,
		target: manager.Target{Source: r.Source, Ref: r.TargetRef, Commit: r.TargetCommit},
	}
}

// planRollback finds what undoing the last change to p would do.
func (m *model) planRollback(p herdr.InstalledPluginInfo) tea.Cmd {
	return m.withSpinner(func() tea.Msg {
		u, err := m.b.PlanRollback(m.ctx, p.PluginID)
		return rollbackMsg{plugin: p, undo: u, err: err}
	})
}

// onRollback opens the preview of a rollback, or for an install, which is
// undone by uninstalling, asks first.
func (m *model) onRollback(msg rollbackMsg) tea.Cmd {
	p := msg.plugin
	switch {
	case msg.err != nil:
		m.setStatus(msg.err.Error(), true)
		return nil
	case msg.undo.Remove:
		if p.PluginID == m.opts.SelfID {
			m.setStatus(manager.ErrSelf.Error(), true)
			return nil
		}
		if !m.idle() {
			return nil
		}
		m.confirm = &confirm{
			prompt: "Uninstall " + p.PluginID + "? That undoes its install",
			run:    func() tea.Cmd { return m.uninstall(p.PluginID) },
		}
		return nil
	}
	u := msg.undo
	c := &pendingChange{kind: manager.KindRollback, plugin: p, target: u.Target, note: u.Describe(), undo: &u}
	d := &detail{crumb: tabNames[tabInstalled], title: "Roll back " + p.Name, loading: true, change: c}
	m.detail, m.screen = d, screenDetail
	return m.withSpinner(m.loadPreview(d, u.Target.Source, u.Target.Commit, ""))
}

// available lists the plugins the last check found updates for, in list
// order.
func (m *model) available() []manager.Checked {
	var out []manager.Checked
	for _, p := range m.installed {
		if ch, ok := m.checks[p.PluginID]; ok && ch.Err == nil && ch.Result.Kind == updates.Available {
			out = append(out, ch)
		}
	}
	return out
}

// checkGaps counts the GitHub plugins whose last check failed, and those the
// last check did not cover.
func (m *model) checkGaps() (failed, unchecked int) {
	for _, p := range m.installed {
		if _, github := source.FromInstalled(p); !github {
			continue
		}
		ch, ok := m.checks[p.PluginID]
		switch {
		case !ok:
			unchecked++
		case ch.Err != nil:
			failed++
		}
	}
	return failed, unchecked
}

// visibleInstalled and visibleEntries apply the tab's filter.
func (m *model) visibleInstalled() []herdr.InstalledPluginInfo {
	query := m.filters[tabInstalled].Value()
	if query == "" {
		return m.installed
	}
	var out []herdr.InstalledPluginInfo
	for _, p := range m.installed {
		if matchesInstalled(p, query) {
			out = append(out, p)
		}
	}
	return out
}

// visibleEntries searches m.entries, which is sorted whenever it is loaded
// or the order changes. The result is kept until the query, the order or the
// entries change, since the view asks for it on every frame.
func (m *model) visibleEntries() []market.Entry {
	query := m.filters[tabBrowse].Value()
	c := &m.search
	if c.valid && c.query == query && c.order == m.order && c.version == m.entriesVersion {
		return c.result
	}
	*c = searchCache{valid: true, query: query, order: m.order, version: m.entriesVersion,
		result: market.Search(m.entries, query, m.order)}
	return c.result
}

// typedSource is an owner/repo[/subdir] typed into the marketplace search
// that no listing matches. enter previews it straight from GitHub, which is
// how a plugin outside the index is installed.
func (m *model) typedSource() (source.GitHub, bool) {
	query := strings.TrimSpace(m.filters[tabBrowse].Value())
	if !strings.Contains(query, "/") || strings.ContainsAny(query, " \t") || len(m.visibleEntries()) > 0 {
		return source.GitHub{}, false
	}
	src, err := source.Parse(query)
	return src, err == nil
}

// sortEntries orders the marketplace list by the current order.
func (m *model) sortEntries() {
	market.Sort(m.entries, m.order)
	m.entriesVersion++
}

func (m *model) rowCount() int {
	if m.tab == tabInstalled {
		return len(m.visibleInstalled())
	}
	return len(m.visibleEntries())
}

// setStatus shows text on the status line, which has room for one line; the
// output screen (o) keeps the whole of an operation's output.
func (m *model) setStatus(text string, isErr bool) {
	m.status, m.statusErr = oneLine(text), isErr
	m.statusGen++
}

// How long a status stays: an error longer, since it may need reading.
const (
	statusTimeout      = 5 * time.Second
	errorStatusTimeout = 10 * time.Second
)

type statusExpiredMsg struct{ gen int }

// expireStatus clears the status set last once it has been shown long
// enough, unless another replaced it meanwhile.
func (m *model) expireStatus() tea.Cmd {
	gen, wait := m.statusGen, statusTimeout
	if m.statusErr {
		wait = errorStatusTimeout
	}
	return tea.Tick(wait, func(time.Time) tea.Msg { return statusExpiredMsg{gen} })
}

// oneLine is the first line of s, made printable, marked when more followed.
func oneLine(s string) string {
	first, rest, more := strings.Cut(strings.TrimSpace(s), "\n")
	first = safe.Line(first)
	if more && strings.TrimSpace(rest) != "" {
		first += " …"
	}
	return first
}
