package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-plugin-manager/internal/manager"
	"github.com/vika2603/herdr-plugin-manager/internal/safe"
	"github.com/vika2603/herdr-plugin-manager/internal/source"
	"github.com/vika2603/herdr-plugin-manager/internal/updates"
)

// liveOp is the running install, update or uninstall. What herdr prints
// collects in it as it runs, so the output screen shows it while it does.
type liveOp struct {
	mu   sync.Mutex
	text bytes.Buffer
	step string

	started time.Time
	cancel  context.CancelFunc
	// stopping is set once the user cancelled the operation. Only Update
	// reads and sets it.
	stopping bool
}

func (l *liveOp) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.text.Write(p)
}

// setStep says which part of a batch runs, such as "2 of 3: o.a".
func (l *liveOp) setStep(step string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.step = step
}

func (l *liveOp) snapshot() (text, step string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.text.String(), l.step
}

// from is what herdr printed after its first off bytes.
func (l *liveOp) from(off int) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.text.Bytes()
	return string(b[min(off, len(b)):])
}

// progress is the step running and the last line herdr printed, without
// copying all it printed.
func (l *liveOp) progress() (step, last string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := bytes.TrimRight(l.text.Bytes(), "\n")
	return l.step, strings.TrimSpace(string(b[bytes.LastIndexByte(b, '\n')+1:]))
}

// opResult is how an operation ended: what the status line says when it
// succeeded, or its error. retry, when set, opens again what starts the
// part that did not succeed, such as its preview, so that it is reviewed
// again before it runs.
type opResult struct {
	done  string
	err   error
	retry func() tea.Cmd
	// show is the plugin whose details open once the list is reloaded,
	// which say how to use a plugin just installed.
	show string
}

type opDoneMsg struct {
	title  string
	output string
	result opResult
}

// operation runs a long backend call with its own context, which ctrl+c
// cancels without leaving the manager. The output screen opens and shows
// what herdr prints as it runs.
func (m *model) operation(title string, run func(ctx context.Context, op *liveOp) opResult) tea.Cmd {
	ctx, cancel := context.WithCancel(m.ctx)
	op := &liveOp{started: m.now(), cancel: cancel}
	m.busy, m.live = title, op
	m.status, m.statusErr = "", false
	m.screen, m.detail, m.review = screenOutput, nil, nil
	m.page, m.outputOffset, m.outputFollow = nil, 0, true
	m.outputLines = outputCache{}
	m.ops.Add(1)
	return func() tea.Msg {
		defer m.ops.Done()
		defer cancel()
		r := run(ctx, op)
		text, _ := op.snapshot()
		return opDoneMsg{title: title, output: text, result: r}
	}
}

// cancelOperation interrupts the running operation, reporting false when
// there is none left to cancel.
func (m *model) cancelOperation() bool {
	op := m.live
	if op == nil || op.stopping {
		return false
	}
	op.stopping = true
	op.cancel()
	return true
}

// onOpDone ends an install, update or uninstall. A failure or cancellation
// shows herdr's output, which ends with where the plugin stands; a success
// returns to the list. Either way the list is reloaded.
func (m *model) onOpDone(msg opDoneMsg) tea.Cmd {
	r := msg.result
	watching := m.screen == screenOutput
	m.busy, m.live = "", nil
	m.output = output{title: msg.title, text: msg.output, err: r.err, retry: r.retry}
	m.outputLines = outputCache{}
	m.output.cancelled = errors.Is(r.err, manager.ErrCancelled)
	switch {
	case r.err != nil:
		verb := "failed"
		if m.output.cancelled {
			verb = "cancelled"
		}
		m.setStatus(msg.title+" "+verb+m.keyHint(actRetry, "retries"), true)
		if !watching {
			m.outputOffset, m.outputFollow = 0, true
		}
		m.screen, m.detail, m.page = screenOutput, nil, &m.output
	default:
		m.setStatus(r.done+m.keyHint(actOutput, "shows herdr's output"), false)
		if watching || m.screen == screenDetail || m.screen == screenReview {
			m.screen, m.detail = screenList, nil
		}
		m.showAfterLoad = r.show
	}
	return m.loadInstalled()
}

// busyLine is the status line of a running operation: its title and step,
// how to cancel it or see its output, and the last line herdr printed.
func (m *model) busyLine() string {
	t := m.theme
	op := m.live
	if op == nil {
		return t.text.Render(m.busy + "…")
	}
	if op.stopping {
		return t.warn.Render("Cancelling: "+m.busy+"…") + t.faint.Render(" · ctrl+c quits")
	}
	step, last := op.progress()
	title := m.busy
	if step != "" {
		title += " (" + step + ")"
	}
	// herdr prints nothing while a build command runs, so the time shows
	// that it still does.
	hints := " " + m.now().Sub(op.started).Truncate(time.Second).String() + " · ctrl+c cancels"
	if m.screen != screenOutput {
		hints += m.keyHint(actOutput, "shows output")
	}
	line := t.text.Render(title+"…") + t.faint.Render(hints)
	if last != "" && m.screen != screenOutput {
		line += t.faint.Render(" · " + safe.Line(last))
	}
	return line
}

// install installs t as plugin id, over existing when that is installed.
func (m *model) install(t installTarget, id string, existing *herdr.InstalledPluginInfo) tea.Cmd {
	return m.operation("Installing "+t.src.String(), func(ctx context.Context, op *liveOp) opResult {
		o := m.b.Apply(ctx, manager.Change{
			Kind: manager.KindInstall, ID: id, Current: existing,
			Target: manager.Target{Source: t.src, Ref: t.ref, Commit: t.commit},
		}, op)
		return opResult{done: o.Summary(), err: o.Error(), retry: func() tea.Cmd { return m.openInstallAt(t.src, t.ref) }, show: id}
	})
}

// applyChange applies a previewed change.
func (m *model) applyChange(c pendingChange) tea.Cmd {
	c.plugin = m.current(c.plugin)
	id := c.plugin.PluginID
	return m.operation(changeVerbs[c.kind][1]+" "+id, func(ctx context.Context, op *liveOp) opResult {
		var o manager.Outcome
		if c.undo != nil {
			o = m.b.Rollback(ctx, *c.undo, op)
		} else {
			o = m.b.Apply(ctx, manager.Change{Kind: c.kind, ID: id, Current: &c.plugin, Target: c.target}, op)
		}
		retry := func() tea.Cmd {
			if c.undo != nil {
				return m.rollback(m.current(c.plugin))
			}
			again := c
			again.plugin, again.fromPreview = m.current(c.plugin), false
			return m.openPendingChange(again)
		}
		return opResult{done: o.Summary(), err: o.Error(), retry: retry}
	})
}

func (m *model) uninstall(id string) tea.Cmd {
	return m.operation("Uninstalling "+id, func(ctx context.Context, op *liveOp) opResult {
		err := m.b.Uninstall(ctx, id, op)
		return opResult{done: "Uninstalled " + id, err: err, retry: func() tea.Cmd {
			if p, ok := m.installedByID(id); ok {
				m.askUninstall(p)
			}
			return nil
		}}
	})
}

// applyReview updates the included plugins at the commits their reviews
// showed, one after another, and reports the rest as skipped. Once
// cancelled, the updates not yet started are not run; a retry reviews the
// failed and unstarted ones again.
func (m *model) applyReview(b *batchReview) tea.Cmd {
	items := append([]reviewItem(nil), b.items...)
	included := b.includedItems()
	return m.operation("Updating "+plural(len(included), "plugin"), func(ctx context.Context, op *liveOp) opResult {
		var failed, interrupted, notRun []string
		done := 0
		for _, it := range items {
			id := it.checked.Plugin.PluginID
			switch {
			case it.review == nil:
				continue
			case !it.review.Ready():
				fmt.Fprintf(op, "== %s: skipped, it cannot be updated: %s\n", id, it.review.Blocker())
				continue
			case it.leftOut:
				fmt.Fprintf(op, "== %s: left out\n", id)
				continue
			case ctx.Err() != nil:
				fmt.Fprintf(op, "== %s: not started, cancelled\n", id)
				notRun = append(notRun, id)
				continue
			}
			done++
			op.setStep(fmt.Sprintf("%d of %d: %s", done, len(included), id))
			fmt.Fprintf(op, "== %s (%d of %d): %s\n", id, done, len(included), it.review.Explain.Headline)
			o := m.b.Apply(ctx, it.review.Change(), op)
			switch err := o.Error(); {
			case errors.Is(err, manager.ErrCancelled):
				fmt.Fprintf(op, "%v\n", err)
				interrupted = append(interrupted, id)
			case err != nil:
				fmt.Fprintf(op, "%v\n", err)
				failed = append(failed, id)
			default:
				fmt.Fprintln(op, o.Summary())
			}
		}
		again := slices.Concat(failed, interrupted, notRun)
		return opResult{
			done:  "Updated " + plural(len(included), "plugin"),
			err:   manager.BatchError(failed, interrupted, notRun),
			retry: func() tea.Cmd { return m.reviewAgain(again) },
		}
	})
}

// reviewAgain reviews the updates of plugins ids that the last check still
// finds.
func (m *model) reviewAgain(ids []string) tea.Cmd {
	var list []manager.Checked
	for _, ch := range m.available() {
		if slices.Contains(ids, ch.Plugin.PluginID) {
			list = append(list, ch)
		}
	}
	if len(list) == 0 {
		m.setStatus("No update of "+strings.Join(ids, ", ")+" is left to retry", false)
		return nil
	}
	return m.openReview(list)
}

// openPendingChange previews c again: at the commit its target names, or
// for a change from its preview, where its ref is now.
func (m *model) openPendingChange(c pendingChange) tea.Cmd {
	d := &detail{crumb: tabNames[tabInstalled], title: changeVerbs[c.kind][2] + " " + c.plugin.Name, loading: true, change: &c}
	m.detail, m.screen = d, screenDetail
	return m.withSpinner(m.loadPreview(d, c.target.Source, c.target.Ref, ""))
}

// openInstallAt previews an install of src at ref.
func (m *model) openInstallAt(src source.GitHub, ref string) tea.Cmd {
	d := &detail{crumb: tabNames[tabBrowse], title: src.String(), loading: true, install: &installTarget{src: src, ref: ref}}
	m.detail, m.screen = d, screenDetail
	return m.withSpinner(m.loadPreview(d, src, ref, ""))
}

func (m *model) installedByID(id string) (herdr.InstalledPluginInfo, bool) {
	i := slices.IndexFunc(m.installed, func(p herdr.InstalledPluginInfo) bool { return p.PluginID == id })
	if i < 0 {
		return herdr.InstalledPluginInfo{}, false
	}
	return m.installed[i], true
}

// retryEntry is what starts a recorded change that did not succeed again:
// its preview, or for an uninstall its confirmation. It is nil for a change
// that succeeded or cannot be started again from here.
func (m *model) retryEntry(e manager.Entry) func() tea.Cmd {
	if !e.Failed() {
		return nil
	}
	p, installed := m.installedByID(e.Plugin)
	switch {
	case e.Kind == manager.KindUninstall:
		if !installed {
			return nil
		}
		return func() tea.Cmd { m.askUninstall(p); return nil }
	case e.Kind == manager.KindRollback:
		if !installed {
			return nil
		}
		return func() tea.Cmd { return m.rollback(p) }
	case e.Target == nil && e.Before != nil && e.After != nil && e.Before.SameRevision(*e.After):
		// The change only enabled or disabled the plugin.
		if !installed {
			return nil
		}
		want := !e.Before.Enabled
		return func() tea.Cmd {
			if current := m.current(p); current.Enabled != want {
				return m.toggle(current)
			}
			m.setStatus(e.Plugin+" is already "+strings.ToLower(enabledWord(want)), false)
			return nil
		}
	case e.Target == nil:
		return nil
	}
	src, err := source.Parse(e.Target.Source)
	if err != nil {
		return nil
	}
	if _, known := changeVerbs[e.Kind]; e.Kind == manager.KindInstall || !installed || !known {
		return func() tea.Cmd { return m.openInstallAt(src, e.Target.Ref) }
	}
	c := pendingChange{kind: e.Kind, plugin: p, target: manager.Target{Source: src, Ref: e.Target.Ref, Commit: e.Target.Commit}}
	if !updates.IsCommit(c.target.Commit) {
		c.fromPreview = true
	}
	return func() tea.Cmd { return m.openPendingChange(c) }
}
