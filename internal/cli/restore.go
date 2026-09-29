package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vika2603/herdr-plugin-manager/internal/manager"
	"github.com/vika2603/herdr-plugin-manager/internal/safe"
)

func (c *cli) exportCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Write the installed plugins to a file hpm restore reads",
		Long: "Write every installed and linked plugin as JSON: its source, the ref it\n" +
			"follows, the commit installed and whether it is enabled. hpm restore reads\n" +
			"the file on another machine. A locally linked plugin is listed with its\n" +
			"directory, which restore cannot bring back.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			exp, err := c.m.Export(cmd.Context())
			if err != nil {
				return err
			}
			if output == "" || output == "-" {
				if err := manager.WriteExport(c.out, exp); err != nil {
					return err
				}
			} else if err := writeExportFile(output, exp); err != nil {
				return err
			}
			c.exportSummary(exp, output)
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "write to this file instead of stdout")
	return cmd
}

// writeExportFile replaces path with exp in one step, so an interrupted
// export does not leave a partial file.
func writeExportFile(path string, exp *manager.Export) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	werr := manager.WriteExport(f, exp)
	if err := errors.Join(werr, f.Close()); err != nil {
		_ = os.Remove(f.Name())
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		_ = os.Remove(f.Name())
		return err
	}
	return nil
}

// exportSummary tells on stderr what was exported and what restore cannot
// install.
func (c *cli) exportSummary(exp *manager.Export, output string) {
	where := "stdout"
	if output != "" && output != "-" {
		where = output
	}
	fmt.Fprintf(c.errOut, "Exported %s to %s.\n", plural(len(exp.Plugins), "plugin"), where)
	var local, other []string
	for _, p := range exp.Plugins {
		switch p.Kind {
		case manager.ExportGitHub:
		case manager.ExportLocal:
			local = append(local, p.ID)
		default:
			other = append(other, p.ID)
		}
	}
	if len(local) > 0 {
		fmt.Fprintf(c.errOut, "Linked locally, which restore lists but cannot install: %s\n", strings.Join(local, ", "))
	}
	if len(other) > 0 {
		fmt.Fprintf(c.errOut, "From a source restore cannot install: %s\n", strings.Join(other, ", "))
	}
}

func (c *cli) restoreCmd() *cobra.Command {
	var (
		yes, dryRun bool
		exclude     []string
	)
	cmd := &cobra.Command{
		Use:   "restore <file> [plugin-id...]",
		Short: "Install the plugins of an export as they were exported",
		Long: "Bring this machine's plugins to what hpm export wrote, for the plugins named\n" +
			"or all of them. Each plugin is installed from its source at the exported\n" +
			"commit, follows the exported ref afterwards, and is enabled or disabled as\n" +
			"exported. Plugins installed here and not in the export are left alone.\n\n" +
			"The plan comes first: what each plugin needs, and which cannot be restored\n" +
			"and why, such as a local link, a source hpm cannot install, the same id\n" +
			"installed here from another source, or a manifest that cannot run here. Then\n" +
			"each install is shown in full, and nothing runs until the plan is confirmed.\n" +
			"No plugin is installed from another source or at another commit in place of\n" +
			"what was exported.\n\n" +
			"Just before each plugin is changed, its record is read again; a plugin that\n" +
			"changed after the plan was made, such as one installed from another source\n" +
			"meanwhile, is left as it is, and restore must be run again to review it.\n\n" +
			"Keeping a plugin disabled, and enabling or disabling one, needs a running\n" +
			"herdr server. Each change, including one that only enables or disables a\n" +
			"plugin, is recorded in the history as a restore, which hpm rollback undoes.\n" +
			"The command exits with an error when any plugin asked for is not as\n" +
			"exported afterwards.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			exp, err := readExportFile(args[0])
			if err != nil {
				return err
			}
			ids, err := restoreIDs(exp, args[1:], exclude)
			if err != nil {
				return err
			}
			plan, err := c.m.PlanRestore(ctx, exp, ids, c.m.HerdrVersion(ctx))
			if err != nil {
				return err
			}
			c.printRestorePlan(args[0], exp, plan)
			for _, it := range plan.Items {
				c.printRestoreItem(it)
			}
			run := slices.DeleteFunc(slices.Clone(plan.Items), func(it manager.RestoreItem) bool { return !it.Runs() })
			if dryRun {
				fmt.Fprintln(c.out, "\nDry run: nothing was changed.")
				return notRestored(plan.Items)
			}
			if len(run) == 0 {
				fmt.Fprintln(c.out, "\nNothing to restore.")
				return notRestored(plan.Items)
			}
			if err := c.confirm(yes, "\nRestore "+plural(len(run), "plugin")+" as planned above?"); err != nil {
				if errors.Is(err, errCancelled) {
					fmt.Fprintln(c.out, "Nothing was changed.")
				}
				return err
			}
			results := c.runRestore(ctx, plan.Items)
			c.printRestoreResults(plan, results)
			return restoreError(plan.Items, results)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "restore without asking")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the plan in full without changing anything")
	cmd.Flags().StringSliceVar(&exclude, "exclude", nil, "leave this plugin out; repeat or separate with commas")
	return cmd
}

func readExportFile(path string) (*manager.Export, error) {
	f, err := os.Open(path) //nolint:gosec // The export file the user named.
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	exp, err := manager.ReadExport(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return exp, nil
}

// restoreIDs are the exported plugins to plan: those named, or all, less
// those excluded; nil means all. Every id named must be in the export.
func restoreIDs(exp *manager.Export, named, exclude []string) ([]string, error) {
	var all []string
	for _, p := range exp.Plugins {
		all = append(all, p.ID)
	}
	for _, id := range slices.Concat(named, exclude) {
		if !slices.Contains(all, id) {
			return nil, fmt.Errorf("the export does not list %s", id)
		}
	}
	if len(named) == 0 && len(exclude) == 0 {
		return nil, nil
	}
	if len(named) == 0 {
		named = all
	}
	ids := []string{}
	for _, id := range named {
		if !slices.Contains(exclude, id) && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (c *cli) printRestorePlan(path string, exp *manager.Export, plan manager.RestorePlan) {
	from := "exported " + exp.ExportedAt.Local().Format(time.DateTime)
	if exp.HerdrVersion != "" {
		from += " with herdr " + safe.Line(exp.HerdrVersion)
	}
	fmt.Fprintf(c.out, "Restore from %s, %s:\n", path, from)
	tw := c.table()
	counts := map[string]int{}
	for _, it := range plan.Items {
		fmt.Fprintf(tw, "  %s\t%s\n", safe.Line(it.Plugin.ID), it.Describe())
		switch {
		case it.Runs():
			counts["to change"]++
		case it.Action == manager.RestoreUnchanged:
			counts["unchanged"]++
		default:
			counts["cannot be restored"]++
		}
	}
	_ = tw.Flush()
	var parts []string
	for _, k := range []string{"to change", "unchanged", "cannot be restored"} {
		if counts[k] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
		}
	}
	if len(parts) > 0 {
		fmt.Fprintln(c.out, strings.Join(parts, ", ")+".")
	}
	if len(plan.Left) > 0 {
		fmt.Fprintf(c.out, "Left out: %s\n", safeList(plan.Left))
	}
	if len(plan.Others) > 0 {
		fmt.Fprintf(c.out, "Installed here and not in the export, left as they are: %s\n", safeList(plan.Others))
	}
}

// printRestoreItem shows an install or change in full: what changes from
// the plugin installed here, and the manifest at the exported commit.
func (c *cli) printRestoreItem(it manager.RestoreItem) {
	if it.Preview == nil {
		return
	}
	fmt.Fprintf(c.out, "\n== %s (%s)\n", safe.Line(it.Plugin.ID), it.Action)
	if it.Explain != nil {
		printSections(c.out, it.Explain.Sections())
		fmt.Fprintln(c.out)
	}
	printSections(c.out, it.Preview.Sections())
	if len(it.Notes) > 0 {
		fmt.Fprintln(c.out)
		printSections(c.out, []manager.Section{{Title: "Restore", Lines: safeLines(it.Notes)}})
	}
	if it.Blocker != "" {
		fmt.Fprintf(c.errOut, "not restoring %s: %s\n", safe.Line(it.Plugin.ID), safe.Line(it.Blocker))
	}
}

// restoreRun is how one planned item ended: its outcome, or that it was not
// started because the run was cancelled first.
type restoreRun struct {
	outcome    manager.Outcome
	notStarted bool
}

// runRestore carries out the items that run, in order. Once ctx is
// cancelled, as by ctrl+c, the items not yet started are not run.
func (c *cli) runRestore(ctx context.Context, items []manager.RestoreItem) map[string]restoreRun {
	results := map[string]restoreRun{}
	for _, it := range items {
		if !it.Runs() {
			continue
		}
		if ctx.Err() != nil {
			results[it.Plugin.ID] = restoreRun{notStarted: true}
			continue
		}
		fmt.Fprintf(c.out, "\nRestoring %s (%s)\n", safe.Line(it.Plugin.ID), it.Action)
		o := c.m.Restore(ctx, it, c.out)
		if err := o.Error(); err != nil {
			fmt.Fprintf(c.errOut, "%v\n", err)
		} else {
			fmt.Fprintln(c.out, o.Summary())
		}
		results[it.Plugin.ID] = restoreRun{outcome: o}
	}
	return results
}

// resultLabel is how one item ended, in a word or two.
func resultLabel(it manager.RestoreItem, r restoreRun, ran bool) string {
	switch {
	case it.Action == manager.RestoreUnchanged:
		return "unchanged"
	case !it.Runs():
		return "not restored"
	case !ran || r.notStarted:
		return "not started"
	}
	switch res := manager.ResultOf(r.outcome); res {
	case manager.ResultDone:
		switch it.Action {
		case manager.RestoreEnable:
			return "enabled"
		case manager.RestoreDisable:
			return "disabled"
		case manager.RestoreInstall, manager.RestoreChange, manager.RestoreUnchanged,
			manager.RestoreConflict, manager.RestoreLocal, manager.RestoreUnsupported:
		}
		return "restored"
	default:
		return string(res)
	}
}

func (c *cli) printRestoreResults(plan manager.RestorePlan, results map[string]restoreRun) {
	fmt.Fprintln(c.out, "\nRestore results:")
	tw := c.table()
	for _, it := range plan.Items {
		r, ran := results[it.Plugin.ID]
		detail := ""
		switch {
		case it.Action == manager.RestoreUnchanged:
			detail = "already as exported"
		case !it.Runs():
			detail = it.Describe()
		case ran && !r.notStarted:
			detail = safe.Line(r.outcome.Summary())
			if r.outcome.Entry != "" {
				detail += " (history " + r.outcome.Entry + ")"
			}
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", safe.Line(it.Plugin.ID), resultLabel(it, r, ran), detail)
	}
	_ = tw.Flush()
}

// notRestored names the items that cannot be restored, nil when there are
// none.
func notRestored(items []manager.RestoreItem) error {
	var ids []string
	for _, it := range items {
		if !it.Runs() && it.Action != manager.RestoreUnchanged {
			ids = append(ids, it.Plugin.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return fmt.Errorf("not restored: %s", safeList(ids))
}

// restoreError names every plugin asked for that is not as exported after
// the run, grouped by why.
func restoreError(items []manager.RestoreItem, results map[string]restoreRun) error {
	groups := map[string][]string{}
	for _, it := range items {
		r, ran := results[it.Plugin.ID]
		switch label := resultLabel(it, r, ran); label {
		case "unchanged", "restored", "enabled", "disabled":
		default:
			groups[label] = append(groups[label], it.Plugin.ID)
		}
	}
	var errs []error
	for _, label := range []string{"failed", "unconfirmed", "cancelled", "not started", "not restored", string(manager.ResultChanged)} {
		if ids := groups[label]; len(ids) > 0 {
			errs = append(errs, fmt.Errorf("%s: %s", label, safeList(ids)))
		}
	}
	if len(groups[string(manager.ResultChanged)]) > 0 {
		errs = append(errs, errors.New("run hpm restore again to review the plan as things are now"))
	}
	return errors.Join(errs...)
}

func safeList(ids []string) string {
	return strings.Join(safeLines(ids), ", ")
}

func safeLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = safe.Line(l)
	}
	return out
}
