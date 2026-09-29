// Package cli is the standalone command line.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-plugin-manager/internal/app"
	"github.com/vika2603/herdr-plugin-manager/internal/config"
	"github.com/vika2603/herdr-plugin-manager/internal/manager"
	"github.com/vika2603/herdr-plugin-manager/internal/market"
	"github.com/vika2603/herdr-plugin-manager/internal/safe"
	"github.com/vika2603/herdr-plugin-manager/internal/source"
	"github.com/vika2603/herdr-plugin-manager/internal/ui"
	"github.com/vika2603/herdr-plugin-manager/internal/updates"
)

// errNeedYes is returned when a confirmation is needed and stdin cannot
// answer it.
var errNeedYes = errors.New("stdin is not a terminal; pass --yes to confirm")

// errCancelled ends a command the user declined.
var errCancelled = errors.New("cancelled")

// TUI runs the interactive manager in the current terminal.
type TUI func(ctx context.Context, m *manager.Manager) error

// cli holds what every command shares.
type cli struct {
	m      *manager.Manager
	in     io.Reader
	out    io.Writer
	errOut io.Writer
	// interactive reports whether in is a terminal that can answer prompts.
	interactive bool
}

// Execute runs the command line with os.Args.
func Execute(ctx context.Context, m *manager.Manager, tui TUI) error {
	c := &cli{m: m, in: os.Stdin, out: os.Stdout, errOut: os.Stderr, interactive: isTerminal(os.Stdin)}
	root := c.root(tui)
	return root.ExecuteContext(ctx)
}

func (c *cli) root(tui TUI) *cobra.Command {
	root := &cobra.Command{
		Use:   app.Name,
		Short: "Browse, install and update herdr plugins",
		Long: "Browse, install and update herdr plugins.\n\n" +
			"Without a command, opens the interactive manager in this terminal.\n" +
			"Installed as a herdr plugin, the same manager opens in a popup.",
		Version:       app.Version,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !isTerminal(os.Stdout) || !c.interactive {
				return errors.New("the interactive manager needs a terminal; see --help for commands")
			}
			return tui(cmd.Context(), c.m)
		},
	}
	root.SetIn(c.in)
	root.SetOut(c.out)
	root.SetErr(c.errOut)
	root.AddCommand(
		c.listCmd(), c.searchCmd(), c.infoCmd(), c.installCmd(), c.uninstallCmd(),
		c.enableCmd(true), c.enableCmd(false), c.outdatedCmd(), c.updateCmd(), c.rollbackCmd(), c.historyCmd(),
		c.versionCmd(manager.KindSwitch), c.versionCmd(manager.KindPin), c.versionCmd(manager.KindUnpin), c.versionCmd(manager.KindReinstall),
		c.logsCmd(), c.keysCmd(),
	)
	return root
}

func (c *cli) listCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List installed and linked plugins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			plugins, err := c.m.Installed(cmd.Context())
			if err != nil {
				return err
			}
			if asJSON {
				return c.writeJSON(plugins)
			}
			if len(plugins) == 0 {
				fmt.Fprintln(c.out, "No plugins installed.")
				return nil
			}
			tw := c.table()
			fmt.Fprintln(tw, "ID\tSTATUS\tVERSION\tSOURCE")
			for _, p := range plugins {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.PluginID, enabledLabel(p.Enabled), p.Version, manager.SourceLabel(p))
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			for _, p := range plugins {
				for _, w := range p.Warnings.ValueOrZero() {
					fmt.Fprintf(c.errOut, "warning: %s: %s\n", p.PluginID, w)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print herdr's plugin records as JSON")
	return cmd
}

// searchResult is the JSON form of a marketplace entry.
type searchResult struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	Description     string   `json:"description"`
	Source          string   `json:"source"`
	URL             string   `json:"url"`
	Stars           int      `json:"stars"`
	PushedAt        string   `json:"pushed_at"`
	MinHerdrVersion string   `json:"min_herdr_version"`
	Platforms       []string `json:"platforms"`
	Installed       bool     `json:"installed"`
}

func (c *cli) searchCmd() *cobra.Command {
	var (
		sortBy  string
		limit   int
		refresh bool
		asJSON  bool
	)
	cmd := &cobra.Command{
		Use:   "search [query...]",
		Short: "Search the herdr plugin marketplace",
		Long: "Search the herdr plugin marketplace index published at " + market.IndexURL + ".\n" +
			"Every query term must appear in the plugin's id, name, description, source,\n" +
			"language or topics. Results are ranked by where the terms appear: the name\n" +
			"and id first, then descriptions, then topics; topics that matched are shown\n" +
			"in brackets. The index is unreviewed: a listing is not an endorsement.",
		RunE: func(cmd *cobra.Command, args []string) error {
			order, ok := market.ParseOrder(sortBy)
			if !ok {
				return fmt.Errorf("unknown sort order %q (relevance, popular, trending, recent, newest, name)", sortBy)
			}
			ctx := cmd.Context()
			ix, err := c.loadIndex(ctx, refresh)
			if err != nil {
				return err
			}
			installed := c.installedIDs(ctx)
			query := strings.Join(args, " ")
			terms := market.Terms(query)
			entries := ix.Entries()
			market.Sort(entries, order)
			entries = market.Search(entries, query, order)
			total := len(entries)
			if limit > 0 && len(entries) > limit {
				entries = entries[:limit]
			}
			if asJSON {
				out := make([]searchResult, len(entries))
				for i, e := range entries {
					out[i] = searchResult{
						ID: e.Manifest.ID, Name: e.Manifest.Name, Version: e.Manifest.Version,
						Description: market.ShownDescription(e, terms), Source: e.Source.String(), URL: e.Source.WebURL(),
						Stars: e.Repo.Stars, PushedAt: e.Repo.PushedAt.Format(time.RFC3339),
						MinHerdrVersion: e.Manifest.MinHerdrVersion, Platforms: e.Manifest.Platforms,
						Installed: installed[e.Manifest.ID],
					}
				}
				return c.writeJSON(out)
			}
			if total == 0 {
				fmt.Fprintln(c.out, "No plugins match.")
				return nil
			}
			tw := c.table()
			fmt.Fprintln(tw, "ID\tVERSION\tSTARS\tSOURCE\tDESCRIPTION")
			shownInstalled := false
			for _, e := range entries {
				id := e.Manifest.ID
				if installed[id] {
					id += " *"
					shownInstalled = true
				}
				desc := market.ShownDescription(e, terms)
				if topics := market.MatchedTopics(e, terms); len(topics) > 0 && !containsTerm(desc, terms) {
					desc = "[" + strings.Join(topics, ", ") + "] " + desc
				}
				fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", id, e.Manifest.Version, e.Repo.Stars, e.Source, market.Snippet(desc, terms, 60))
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			if len(entries) < total {
				fmt.Fprintf(c.errOut, "Showing %d of %d matches; use --limit 0 for all.\n", len(entries), total)
			}
			if shownInstalled {
				fmt.Fprintln(c.errOut, "* installed")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&sortBy, "sort", "relevance", "order: relevance (popular without a query), popular, trending, recent, newest or name")
	cmd.Flags().IntVar(&limit, "limit", 20, "maximum number of results; 0 for all")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "download the index even if the cached copy is fresh")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print results as JSON")
	return cmd
}

func (c *cli) infoCmd() *cobra.Command {
	var ref string
	cmd := &cobra.Command{
		Use:   "info <plugin-id | owner/repo[/subdir]>",
		Short: "Show an installed plugin, or preview one from GitHub",
		Long: "Show an installed plugin by id. Otherwise read the manifest of a marketplace\n" +
			"plugin or a GitHub source and show what installing it would run.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			plugins, _ := c.m.Installed(ctx)
			for _, p := range plugins {
				if p.PluginID == args[0] && ref == "" {
					c.printInstalled(ctx, p)
					return nil
				}
			}
			src, hint, err := c.resolve(ctx, args[0], ref)
			if err != nil {
				return err
			}
			preview, err := c.m.Preview(ctx, src, ref, hint, c.m.HerdrVersion(ctx), plugins)
			if err != nil {
				return err
			}
			printSections(c.out, preview.Sections())
			return nil
		},
	}
	cmd.Flags().StringVar(&ref, "ref", "", "branch, tag or commit to preview (default: what install would pick)")
	return cmd
}

func (c *cli) printInstalled(ctx context.Context, p herdr.InstalledPluginInfo) {
	lines := []string{
		fmt.Sprintf("%s %s (%s)", p.Name, p.Version, p.PluginID),
		"status: " + enabledLabel(p.Enabled),
		"source: " + manager.SourceLabel(p),
		"root: " + p.PluginRoot,
	}
	if d := p.Description.ValueOrZero(); d != "" {
		lines = append(lines, d)
	}
	sections := []manager.Section{{Title: "Plugin", Lines: lines}}
	if w := p.Warnings.ValueOrZero(); len(w) > 0 {
		sections = append(sections, manager.Section{Title: "Warnings", Lines: w})
	}
	if _, ok := source.FromInstalled(p); ok {
		res, err := c.m.Check(ctx, p)
		line := res.Describe()
		if err != nil {
			line = "check failed: " + safe.Line(err.Error())
		}
		sections = append(sections, manager.Section{Title: "Update", Lines: []string{line, "updates: " + safe.Line(manager.TrackingOf(p).Describe())}})
	}
	printSections(c.out, sections)
}

func (c *cli) installCmd() *cobra.Command {
	var (
		ref string
		yes bool
	)
	cmd := &cobra.Command{
		Use:   "install <plugin-id | owner/repo[/subdir]>",
		Short: "Preview and install a plugin from GitHub",
		Long: "Install a marketplace plugin by id, or any GitHub plugin by its\n" +
			"owner/repo[/subdir] source. The manifest is shown first: its build commands\n" +
			"run during install and its startup commands in every herdr session.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			src, hint, err := c.resolve(ctx, args[0], ref)
			if err != nil {
				return err
			}
			plugins, err := c.m.Installed(ctx)
			if err != nil {
				fmt.Fprintf(c.errOut, "warning: could not list installed plugins: %v\n", err)
			}
			preview, err := c.m.Preview(ctx, src, ref, hint, c.m.HerdrVersion(ctx), plugins)
			if err != nil {
				return err
			}
			printSections(c.out, preview.Sections())
			if len(preview.Problems) > 0 {
				return errors.New("not installing: see the problems above")
			}
			if err := c.confirm(yes, "Install "+preview.Manifest.ID+"?"); err != nil {
				return err
			}
			o := c.m.Apply(ctx, manager.Change{
				Kind: manager.KindInstall, ID: preview.Manifest.ID, Current: preview.Existing,
				Target: manager.Target{Source: src, Ref: preview.Ref, Commit: preview.Commit},
			}, c.out)
			return c.report(o)
		},
	}
	cmd.Flags().StringVar(&ref, "ref", "", "branch, tag or commit to install (default: the latest release of a plugin at the repository root, else the default branch)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "install without asking")
	return cmd
}

func (c *cli) uninstallCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "uninstall <plugin-id>",
		Short: "Uninstall a GitHub plugin or unlink a local one",
		Long: "Uninstall a plugin. A GitHub install loses its managed checkout; a locally\n" +
			"linked plugin is only unregistered and its directory is left alone.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := c.confirm(yes, "Uninstall "+args[0]+"?"); err != nil {
				return err
			}
			return c.m.Uninstall(cmd.Context(), args[0], c.out)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "uninstall without asking")
	return cmd
}

func (c *cli) enableCmd(enable bool) *cobra.Command {
	use, short := "disable", "Disable a plugin (needs a running herdr server)"
	if enable {
		use, short = "enable", "Enable a plugin (needs a running herdr server)"
	}
	return &cobra.Command{
		Use:   use + " <plugin-id>...",
		Short: short,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, id := range args {
				if err := c.m.SetEnabled(cmd.Context(), id, enable); err != nil {
					return fmt.Errorf("%s %s: %w", use, id, err)
				}
				fmt.Fprintf(c.out, "%s %s\n", enabledLabel(enable), id)
			}
			return nil
		},
	}
}

// outdatedResult is the JSON form of an update check.
type outdatedResult struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	Source        string `json:"source,omitempty"`
	CurrentRef    string `json:"current_ref,omitempty"`
	CurrentCommit string `json:"current_commit,omitempty"`
	TargetRef     string `json:"target_ref,omitempty"`
	TargetCommit  string `json:"target_commit,omitempty"`
	Error         string `json:"error,omitempty"`
}

func (c *cli) outdatedCmd() *cobra.Command {
	var (
		asJSON bool
		all    bool
	)
	cmd := &cobra.Command{
		Use:   "outdated",
		Short: "Check installed GitHub plugins for updates",
		Long: "Compare each GitHub-installed plugin with its remote. A plugin installed from a\n" +
			"release tag is compared with the newest release tag; one installed from a branch\n" +
			"or the default branch with that branch's current commit; for a plugin in a\n" +
			"subdirectory, only commits that change the subdirectory count. Commit pins are skipped.\n" +
			"Exits with an error when any check fails.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			plugins, err := c.m.Installed(ctx)
			if err != nil {
				return err
			}
			checked := c.m.CheckAll(ctx, plugins)
			if asJSON {
				out := make([]outdatedResult, len(checked))
				for i, ch := range checked {
					r := ch.Result
					out[i] = outdatedResult{
						ID: ch.Plugin.PluginID, Status: string(r.Kind), CurrentRef: r.CurrentRef, CurrentCommit: r.CurrentCommit,
						TargetRef: r.TargetRef, TargetCommit: r.TargetCommit,
					}
					if r.Kind != updates.Local {
						out[i].Source = r.Source.String()
					}
					if ch.Err != nil {
						out[i].Status, out[i].Error = "error", ch.Err.Error()
					}
				}
				if err := c.writeJSON(out); err != nil {
					return err
				}
				return checkError(checked)
			}
			tw := c.table()
			fmt.Fprintln(tw, "ID\tSTATUS\tDETAIL")
			available := 0
			for _, ch := range checked {
				switch {
				case ch.Err != nil:
					fmt.Fprintf(tw, "%s\terror\t%s\n", ch.Plugin.PluginID, ch.Err)
				case ch.Result.Kind == updates.Available:
					available++
					fmt.Fprintf(tw, "%s\tupdate\t%s\n", ch.Plugin.PluginID, ch.Result.Describe())
				case all:
					fmt.Fprintf(tw, "%s\t%s\t%s\n", ch.Plugin.PluginID, ch.Result.Kind, ch.Result.Describe())
				}
			}
			failed := checkError(checked)
			if available == 0 && !all && failed == nil {
				fmt.Fprintln(c.out, "All plugins are up to date.")
				return nil
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			return failed
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print every result as JSON")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "also list plugins without updates")
	return cmd
}

func (c *cli) updateCmd() *cobra.Command {
	var (
		yes, dryRun bool
		exclude     []string
	)
	cmd := &cobra.Command{
		Use:   "update [plugin-id...]",
		Short: "Update GitHub plugins by reinstalling them",
		Long: "Update the given plugins, or every plugin with an update. herdr has no update\n" +
			"command, so each plugin is reinstalled at its new target. The updates are\n" +
			"listed first, then each is shown in full: what changes, its release notes or\n" +
			"commits, and the new manifest, whose build commands run again. herdr is asked\n" +
			"for the commit shown, so what builds is what was shown, whatever the ref\n" +
			"points at by then.\n\n" +
			"A disabled plugin stays disabled, which needs a running herdr server; without\n" +
			"one it is not updated. Each update reports the plugin's state after it.\n" +
			"Plugins whose check succeeded are still updated when others fail; the command\n" +
			"then exits with an error.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			plugins, err := c.m.Installed(ctx)
			if err != nil {
				return err
			}
			selected, err := selectPlugins(plugins, args)
			if err != nil {
				return err
			}
			if _, err := selectPlugins(plugins, exclude); err != nil {
				return fmt.Errorf("--exclude: %w", err)
			}
			checked := c.m.CheckAll(ctx, selected)
			pending := c.pending(checked, len(args) > 0, exclude)
			checkErr := checkError(checked)
			if len(pending) == 0 {
				if checkErr != nil {
					return checkErr
				}
				fmt.Fprintln(c.out, "Nothing to update.")
				return nil
			}
			reviews := c.m.ReviewAll(ctx, pending, c.m.HerdrVersion(ctx))
			c.printPlan(reviews)
			var blocked []string
			for _, r := range reviews {
				c.printReview(r)
				if !r.Ready() {
					blocked = append(blocked, r.Checked.Plugin.PluginID)
				}
			}
			var blockErr error
			if len(blocked) > 0 {
				blockErr = fmt.Errorf("cannot update: %s", strings.Join(blocked, ", "))
			}
			if dryRun {
				fmt.Fprintln(c.out, "\nDry run: nothing was changed.")
				return errors.Join(checkErr, blockErr)
			}
			failErr, err := c.applyReviews(ctx, reviews, yes)
			if err != nil {
				return err
			}
			return errors.Join(checkErr, blockErr, failErr)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "update without asking")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the updates in full without applying them")
	cmd.Flags().StringSliceVar(&exclude, "exclude", nil, "leave this plugin out; repeat or separate with commas")
	return cmd
}

// pending are the updates the checks found, less those excluded. It reports
// each failed check, each excluded update and, when the plugins were named,
// each plugin without an update.
func (c *cli) pending(checked []manager.Checked, named bool, exclude []string) []manager.Checked {
	var out []manager.Checked
	for _, ch := range checked {
		id := ch.Plugin.PluginID
		switch {
		case ch.Err != nil:
			fmt.Fprintf(c.errOut, "check failed: %v\n", ch.Err)
		case ch.Result.Kind != updates.Available:
			if named {
				fmt.Fprintf(c.out, "%s: %s\n", id, ch.Result.Describe())
			}
		case slices.Contains(exclude, id):
			fmt.Fprintf(c.out, "%s: excluded (%s)\n", id, ch.Result.Describe())
		default:
			out = append(out, ch)
		}
	}
	return out
}

// applyReviews applies the reviewed updates that can run, returning which
// failed, or an error when the run had to stop.
func (c *cli) applyReviews(ctx context.Context, reviews []manager.Review, yes bool) (failed, stop error) {
	var ids []string
	for _, r := range reviews {
		if !r.Ready() {
			continue
		}
		ok, err := c.applyReview(ctx, r, yes)
		if err != nil {
			return nil, err
		}
		if !ok {
			ids = append(ids, r.Checked.Plugin.PluginID)
		}
	}
	if len(ids) > 0 {
		return fmt.Errorf("not updated: %s", strings.Join(ids, ", ")), nil
	}
	return nil, nil
}

// printPlan lists the updates about to be reviewed, one line each.
func (c *cli) printPlan(reviews []manager.Review) {
	fmt.Fprintf(c.out, "%s:\n", plural(len(reviews), "update"))
	tw := c.table()
	for _, r := range reviews {
		what := r.Checked.Result.Describe()
		if r.Err == nil {
			what = r.Explain.Headline
			if len(r.Explain.Runs) > 0 {
				what += "; what it runs changes"
			}
		}
		if b := r.Blocker(); b != "" {
			what = "cannot update: " + b
		}
		fmt.Fprintf(tw, "  %s\t%s\n", r.Checked.Plugin.PluginID, safe.Line(what))
	}
	_ = tw.Flush()
}

// printReview shows one update in full.
func (c *cli) printReview(r manager.Review) {
	id := r.Checked.Plugin.PluginID
	fmt.Fprintf(c.out, "\n== %s\n", id)
	if r.Err != nil {
		fmt.Fprintf(c.errOut, "%s: %v\n", id, r.Err)
		return
	}
	printSections(c.out, r.Explain.Sections())
	fmt.Fprintln(c.out)
	printSections(c.out, r.Preview.Sections())
	if len(r.Preview.Problems) > 0 {
		fmt.Fprintf(c.errOut, "skipping %s: see the problems above\n", id)
	}
}

// applyReview applies one reviewed update. It reports false for an update
// that could not be applied, and returns an error only when the whole run
// must stop, such as a confirmation stdin cannot answer. A declined update
// counts as handled.
func (c *cli) applyReview(ctx context.Context, r manager.Review, yes bool) (bool, error) {
	id := r.Checked.Plugin.PluginID
	if err := c.confirm(yes, "\nUpdate "+id+"?"); err != nil {
		if errors.Is(err, errCancelled) {
			return true, nil
		}
		return false, err
	}
	fmt.Fprintf(c.out, "\nUpdating %s\n", id)
	o := c.m.Apply(ctx, r.Change(), c.out)
	if err := o.Error(); err != nil {
		fmt.Fprintf(c.errOut, "%v\n", err)
		return false, nil
	}
	fmt.Fprintln(c.out, o.Summary())
	return true, nil
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// report prints where a change left the plugin, returning its error when it
// failed.
func (c *cli) report(o manager.Outcome) error {
	if err := o.Error(); err != nil {
		return err
	}
	fmt.Fprintln(c.out, o.Summary())
	return nil
}

// versionCmds describe the commands that move an installed plugin to
// another version or change how it follows new ones.
var versionCmds = map[manager.ChangeKind]struct {
	use, short, long string
	args             cobra.PositionalArgs
}{
	manager.KindSwitch: {
		"switch <plugin-id> <ref>", "Install another version of a plugin: a release, a branch or a commit",
		"Reinstall a plugin at ref, which it then follows: a release tag follows newer\n" +
			"releases, a branch or another tag follows it when it moves, and a commit is a\n" +
			"pin with no updates. An older release downgrades the plugin.",
		cobra.ExactArgs(2),
	},
	manager.KindPin: {
		"pin <plugin-id>", "Hold a plugin at the commit it is installed at",
		"Reinstall a plugin at the commit it is installed at, recorded as a pin, so that\n" +
			"it has no updates until it is unpinned.",
		cobra.ExactArgs(1),
	},
	manager.KindUnpin: {
		"unpin <plugin-id> [ref]", "Let a pinned plugin follow a release, branch or tag again",
		"Reinstall a pinned plugin at ref, which it then follows. Without ref it follows\n" +
			"what an install picks: the latest release of a plugin at the repository root,\n" +
			"else the default branch. The version installed is where that ref is now.",
		cobra.RangeArgs(1, 2),
	},
	manager.KindReinstall: {
		"reinstall <plugin-id>", "Reinstall a plugin at the version it is installed at",
		"Reinstall a plugin at the commit it is installed at, running its build\n" +
			"commands again. It keeps following what it follows.",
		cobra.ExactArgs(1),
	},
}

// versionCmd is the command for a version change. Like an update, it
// shows the manifest it installs first and keeps the plugin enabled or
// disabled.
func (c *cli) versionCmd(kind manager.ChangeKind) *cobra.Command {
	d := versionCmds[kind]
	var yes bool
	cmd := &cobra.Command{
		Use: d.use, Short: d.short, Long: d.long, Args: d.args,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id := args[0]
			p, err := c.m.Find(ctx, id)
			if err != nil {
				return err
			}
			if p == nil {
				return fmt.Errorf("plugin %q is not installed", id)
			}
			var ref string
			if len(args) > 1 {
				ref = args[1]
			}
			if ref, err = manager.VersionRef(*p, kind, ref); err != nil {
				return err
			}
			src, _ := source.FromInstalled(*p)
			var preview *manager.Preview
			if kind == manager.KindReinstall {
				preview, err = c.m.PreviewAt(ctx, src, ref, manager.ReinstallCommit(*p), c.m.HerdrVersion(ctx), nil)
			} else {
				preview, err = c.m.Preview(ctx, src, ref, "", c.m.HerdrVersion(ctx), nil)
			}
			if err != nil {
				return err
			}
			preview.RequireID(id)
			fmt.Fprintf(c.out, "%s now: %s; %s\n\n", id, manager.StateOf(*p), manager.TrackingOf(*p).Describe())
			printSections(c.out, c.m.Explain(ctx, *p, preview).Sections())
			fmt.Fprintln(c.out)
			printSections(c.out, preview.Sections())
			if len(preview.Problems) > 0 {
				return fmt.Errorf("not changing %s: see the problems above", id)
			}
			if err := c.confirm(yes, fmt.Sprintf("%s %s?", capitalize(string(kind)), id)); err != nil {
				return err
			}
			return c.report(c.m.Apply(ctx, manager.Change{
				Kind: kind, ID: id, Current: p,
				Target: manager.Target{Source: src, Ref: preview.Ref, Commit: preview.Commit},
			}, c.out))
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "go ahead without asking")
	return cmd
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func (c *cli) rollbackCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "rollback <plugin-id>",
		Short: "Undo the last change this manager made to a plugin",
		Long: "Take a plugin back to where it was before the last install, update or other\n" +
			"change recorded in the history, enabled or disabled as it was. An install is\n" +
			"undone by uninstalling. The earlier commit is reinstalled, so its manifest is\n" +
			"shown first, and the plugin follows the ref it followed then. A plugin changed\n" +
			"outside this manager is left alone.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			u, err := c.m.PlanRollback(ctx, args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(c.out, "To "+u.Describe()+".")
			question := "Roll back " + args[0] + "?"
			if u.Remove {
				question = "Uninstall " + args[0] + "?"
			} else {
				preview, err := c.m.PreviewAt(ctx, u.Target.Source, u.Target.Ref, u.Target.Commit, c.m.HerdrVersion(ctx), nil)
				if err != nil {
					return err
				}
				preview.RequireID(args[0])
				fmt.Fprintln(c.out)
				if u.Current != nil {
					printSections(c.out, c.m.Explain(ctx, *u.Current, preview).Sections())
					fmt.Fprintln(c.out)
				}
				printSections(c.out, preview.Sections())
				if len(preview.Problems) > 0 {
					return errors.New("not rolling back: see the problems above")
				}
			}
			if err := c.confirm(yes, question); err != nil {
				return err
			}
			return c.report(c.m.Rollback(ctx, u, c.out))
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "roll back without asking")
	return cmd
}

func (c *cli) historyCmd() *cobra.Command {
	var (
		asJSON bool
		limit  int
		show   string
	)
	cmd := &cobra.Command{
		Use:   "history [plugin-id]",
		Short: "List the changes this manager made to plugins",
		Long: "List the installs, updates, rollbacks and uninstalls this manager made, the\n" +
			"newest first, with each plugin's state before and after. --show prints one\n" +
			"change in full, with everything herdr printed during it. The history is kept\n" +
			"in $XDG_STATE_HOME/" + "herdr-plugin-manager, or ~/.local/state/herdr-plugin-manager.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			entries, err := c.m.HistoryEntries()
			if err != nil {
				return err
			}
			if show != "" {
				return c.showEntry(entries, show)
			}
			slices.Reverse(entries)
			if len(args) == 1 {
				entries = slices.DeleteFunc(entries, func(e manager.Entry) bool { return e.Plugin != args[0] })
			}
			if limit > 0 && len(entries) > limit {
				entries = entries[:limit]
			}
			if asJSON {
				if entries == nil {
					entries = []manager.Entry{}
				}
				return c.writeJSON(entries)
			}
			if len(entries) == 0 {
				fmt.Fprintln(c.out, "No changes recorded.")
				return nil
			}
			tw := c.table()
			fmt.Fprintln(tw, "ENTRY\tTIME\tKIND\tPLUGIN\tRESULT\tCHANGE")
			for _, e := range entries {
				result := "done"
				switch {
				case e.AfterUnknown:
					result = "unconfirmed"
				case e.Failed():
					result = "failed"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", e.ID, e.Time.Local().Format(time.DateTime), e.Kind, e.Plugin, result, change(e))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the entries as JSON")
	cmd.Flags().IntVar(&limit, "limit", 20, "maximum number of entries; 0 for all")
	cmd.Flags().StringVar(&show, "show", "", "print the entry with this id in full, with herdr's output")
	return cmd
}

func (c *cli) showEntry(entries []manager.Entry, id string) error {
	i := slices.IndexFunc(entries, func(e manager.Entry) bool { return e.ID == id })
	if i < 0 {
		return fmt.Errorf("no history entry %q", id)
	}
	e := entries[i]
	fmt.Fprintf(c.out, "%s %s %s\n", e.Time.Local().Format(time.DateTime), e.Kind, e.Plugin)
	if e.Target != nil {
		fmt.Fprintf(c.out, "target: %s @ %s\n", e.Target.Source, manager.RevisionLabel(e.Target.Ref, e.Target.Commit))
	}
	fmt.Fprintln(c.out, "before: "+stateLabel(e.Before, false))
	fmt.Fprintln(c.out, "after: "+stateLabel(e.After, e.AfterUnknown))
	if e.Failed() {
		fmt.Fprintln(c.out, "error: "+safe.Text(e.Error))
	}
	if e.Log == "" {
		return nil
	}
	log, err := manager.ReadLog(e)
	if err != nil {
		return err
	}
	fmt.Fprintln(c.out, "\nOutput:")
	fmt.Fprintln(c.out, safe.Text(log))
	return nil
}

// change is an entry's before and after, on one line.
func change(e manager.Entry) string {
	return stateLabel(e.Before, false) + " -> " + stateLabel(e.After, e.AfterUnknown)
}

func stateLabel(s *manager.State, unknown bool) string {
	switch {
	case unknown:
		return "unknown"
	case s == nil:
		return "not installed"
	}
	return s.String()
}

func (c *cli) logsCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "logs <plugin-id>",
		Short: "Show the commands herdr ran for a plugin (needs a running herdr server)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			logs, err := c.m.Logs(cmd.Context(), args[0], limit)
			if err != nil {
				return err
			}
			if len(logs) == 0 {
				fmt.Fprintln(c.out, "No command logs.")
				return nil
			}
			for _, l := range logs {
				fmt.Fprintln(c.out, manager.LogHeader(l))
				for _, stream := range []struct{ name, text string }{{"stdout", l.Stdout.ValueOrZero()}, {"stderr", l.Stderr.ValueOrZero()}} {
					if text := strings.TrimRight(stream.text, "\n"); text != "" {
						fmt.Fprintf(c.out, "  %s:\n", stream.name)
						for line := range strings.SplitSeq(text, "\n") {
							fmt.Fprintf(c.out, "    %s\n", line)
						}
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "maximum number of log entries")
	return cmd
}

func (c *cli) keysCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "keys",
		Short: "List the keys of the interactive manager, and the config file",
		Long: "List what each key does in the popup and in the terminal manager. To change\n" +
			"them, give an action its keys in the [keys] table of the config file, such as\n" +
			"  install = [\"I\"]\n" +
			"An empty list unbinds the action.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := app.ConfigDir(cmd.Context(), c.m)
			cfg, cfgErr := config.Load(dir)
			keys, keysErr := ui.Keys(cfg.Keys)
			if dir == "" {
				fmt.Fprintln(c.out, "Config file: unknown; herdr could not report the plugin config directory")
			} else {
				fmt.Fprintf(c.out, "Config file: %s\n", filepath.Join(dir, config.File))
			}
			tw := c.table()
			fmt.Fprintln(tw, "\nACTION\tKEYS")
			for _, k := range keys {
				fmt.Fprintf(tw, "%s\t%s\n", k.Action, strings.Join(k.Keys, "  "))
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			if err := errors.Join(cfgErr, keysErr); err != nil {
				return fmt.Errorf("%w; the keys above are the defaults", err)
			}
			return nil
		},
	}
}

// resolve turns a plugin id from the marketplace or an owner/repo[/subdir]
// source into a GitHub source. For the default branch it also returns the
// head commit the marketplace index recorded, which Preview reads the
// manifest at while it resolves the branch; a source is only looked up in an
// index already cached, since downloading one would cost more than it saves.
func (c *cli) resolve(ctx context.Context, arg, ref string) (source.GitHub, string, error) {
	var (
		src source.GitHub
		ix  *market.Index
		err error
	)
	if strings.Contains(arg, "/") {
		if src, err = source.Parse(arg); err != nil {
			return source.GitHub{}, "", err
		}
		ix, _ = c.m.Market.Cached()
		arg = src.String()
	} else if ix, err = c.loadIndex(ctx, false); err != nil {
		return source.GitHub{}, "", err
	}
	var e market.Entry
	found := false
	if ix != nil {
		e, found = market.Find(ix.Entries(), arg)
	}
	switch {
	case found && ref == "":
		return e.Source, e.Repo.HeadCommit, nil
	case found:
		return e.Source, "", nil
	case src.Repo != "":
		return src, "", nil
	}
	return source.GitHub{}, "", fmt.Errorf("no marketplace plugin has id %q; give its owner/repo[/subdir] source instead", arg)
}

func (c *cli) loadIndex(ctx context.Context, refresh bool) (*market.Index, error) {
	ix, st, err := c.m.Index(ctx, refresh)
	if err != nil {
		return nil, err
	}
	if st.FetchErr != nil {
		fmt.Fprintf(c.errOut, "warning: using the index cached at %s: %v\n", st.FetchedAt.Format(time.DateTime), st.FetchErr)
	}
	return ix, nil
}

func (c *cli) installedIDs(ctx context.Context) map[string]bool {
	ids := map[string]bool{}
	plugins, _ := c.m.Installed(ctx)
	for _, p := range plugins {
		ids[p.PluginID] = true
	}
	return ids
}

func (c *cli) confirm(yes bool, question string) error {
	if yes {
		return nil
	}
	if !c.interactive {
		return errNeedYes
	}
	fmt.Fprintf(c.out, "%s [y/N] ", question)
	line, err := bufio.NewReader(c.in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	}
	return errCancelled
}

func (c *cli) table() *tabwriter.Writer {
	return tabwriter.NewWriter(c.out, 0, 0, 2, ' ', 0)
}

func (c *cli) writeJSON(v any) error {
	enc := json.NewEncoder(c.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func selectPlugins(plugins []herdr.InstalledPluginInfo, ids []string) ([]herdr.InstalledPluginInfo, error) {
	if len(ids) == 0 {
		return plugins, nil
	}
	byID := map[string]herdr.InstalledPluginInfo{}
	for _, p := range plugins {
		byID[p.PluginID] = p
	}
	out := make([]herdr.InstalledPluginInfo, 0, len(ids))
	for _, id := range ids {
		p, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("plugin %q is not installed", id)
		}
		out = append(out, p)
	}
	return out, nil
}

// checkError names the plugins whose update check failed, or is nil when
// every check succeeded.
func checkError(checked []manager.Checked) error {
	var ids []string
	for _, ch := range checked {
		if ch.Err != nil {
			ids = append(ids, ch.Plugin.PluginID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return fmt.Errorf("update check failed: %s", strings.Join(ids, ", "))
}

func printSections(w io.Writer, sections []manager.Section) {
	for i, s := range sections {
		if i > 0 {
			fmt.Fprintln(w)
		}
		if s.Note != "" {
			fmt.Fprintf(w, "%s (%s)\n", s.Title, s.Note)
		} else {
			fmt.Fprintln(w, s.Title)
		}
		for _, line := range s.Lines {
			fmt.Fprintln(w, "  "+line)
		}
	}
}

func enabledLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func containsTerm(s string, terms []string) bool {
	s = strings.ToLower(s)
	for _, t := range terms {
		if strings.Contains(s, t) {
			return true
		}
	}
	return false
}

func isTerminal(f *os.File) bool {
	return term.IsTerminal(f.Fd())
}
