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
	"strings"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-plugin-manager/internal/app"
	"github.com/vika2603/herdr-plugin-manager/internal/manager"
	"github.com/vika2603/herdr-plugin-manager/internal/market"
	"github.com/vika2603/herdr-plugin-manager/internal/safe"
	"github.com/vika2603/herdr-plugin-manager/internal/source"
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
		c.enableCmd(true), c.enableCmd(false), c.outdatedCmd(), c.updateCmd(), c.logsCmd(),
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
	cmd.Flags().StringVar(&ref, "ref", "", "branch, tag or commit to preview (default: the default branch)")
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
		sections = append(sections, manager.Section{Title: "Update", Lines: []string{line}})
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
			return c.m.Install(ctx, src, ref, preview.Commit, c.out)
		},
	}
	cmd.Flags().StringVar(&ref, "ref", "", "branch, tag or commit to install (default: the default branch)")
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
			"subdirectory, only commits that change the subdirectory count. Commit pins are skipped.",
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
				return c.writeJSON(out)
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
			if available == 0 && !all && !hasErrors(checked) {
				fmt.Fprintln(c.out, "All plugins are up to date.")
				return nil
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print every result as JSON")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "also list plugins without updates")
	return cmd
}

func (c *cli) updateCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "update [plugin-id...]",
		Short: "Update GitHub plugins by reinstalling them",
		Long: "Update the given plugins, or every plugin with an update. herdr has no update\n" +
			"command, so each plugin is reinstalled at its new target; the new manifest is\n" +
			"shown first because its build commands run again. A disabled plugin stays\n" +
			"disabled, which needs a running herdr server.",
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
			var pending []manager.Checked
			for _, ch := range c.m.CheckAll(ctx, selected) {
				switch {
				case ch.Err != nil:
					fmt.Fprintf(c.errOut, "%s: %v\n", ch.Plugin.PluginID, ch.Err)
				case ch.Result.Kind == updates.Available:
					pending = append(pending, ch)
				case len(args) > 0:
					fmt.Fprintf(c.out, "%s: %s\n", ch.Plugin.PluginID, ch.Result.Describe())
				}
			}
			if len(pending) == 0 {
				fmt.Fprintln(c.out, "Nothing to update.")
				return nil
			}
			version := c.m.HerdrVersion(ctx)
			var failed []string
			for _, ch := range pending {
				ok, err := c.updateOne(ctx, ch, version, yes)
				if err != nil {
					return err
				}
				if !ok {
					failed = append(failed, ch.Plugin.PluginID)
				}
			}
			if len(failed) > 0 {
				return fmt.Errorf("not updated: %s", strings.Join(failed, ", "))
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "update without asking")
	return cmd
}

// updateOne previews and applies one update. It reports false for an update
// that could not be applied, and returns an error only when the whole run
// must stop, such as a confirmation stdin cannot answer. A declined update
// counts as handled.
func (c *cli) updateOne(ctx context.Context, ch manager.Checked, herdrVersion string, yes bool) (bool, error) {
	id := ch.Plugin.PluginID
	fmt.Fprintf(c.out, "\n%s: %s\n", id, ch.Result.Describe())
	preview, err := c.m.Preview(ctx, ch.Result.Source, ch.Result.TargetCommit, "", herdrVersion, nil)
	if err != nil {
		fmt.Fprintf(c.errOut, "%v\n", err)
		return false, nil
	}
	preview.RequireID(id)
	printSections(c.out, preview.Sections())
	if len(preview.Problems) > 0 {
		fmt.Fprintf(c.errOut, "skipping %s: see the problems above\n", id)
		return false, nil
	}
	if err := c.confirm(yes, "Update "+id+"?"); err != nil {
		if errors.Is(err, errCancelled) {
			return true, nil
		}
		return false, err
	}
	if err := c.m.Update(ctx, ch.Plugin, ch.Result, c.out); err != nil {
		fmt.Fprintf(c.errOut, "%v\n", err)
		return false, nil
	}
	return true, nil
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

func hasErrors(checked []manager.Checked) bool {
	for _, ch := range checked {
		if ch.Err != nil {
			return true
		}
	}
	return false
}

func printSections(w io.Writer, sections []manager.Section) {
	for i, s := range sections {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, s.Title)
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
