package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/vika2603/herdr-plugin-manager/internal/manager"
	"github.com/vika2603/herdr-plugin-manager/internal/safe"
)

// batchReview is the review of the available updates before they run
// together: what each changes, and which to leave out.
type batchReview struct {
	items          []reviewItem
	cursor, offset int
}

type reviewItem struct {
	checked manager.Checked
	// review is nil while it is read.
	review *manager.Review
	// leftOut is the user's choice not to apply the update.
	leftOut bool
}

// included reports whether the update runs when the review is applied.
func (it reviewItem) included() bool {
	return it.review != nil && it.review.Ready() && !it.leftOut
}

type reviewMsg struct {
	b      *batchReview
	i      int
	review manager.Review
}

// loading counts the updates still being read.
func (b *batchReview) loading() int {
	n := 0
	for _, it := range b.items {
		if it.review == nil {
			n++
		}
	}
	return n
}

func (b *batchReview) includedItems() []reviewItem {
	var out []reviewItem
	for _, it := range b.items {
		if it.included() {
			out = append(out, it)
		}
	}
	return out
}

// openReview opens the review of list and reads each update's manifest and
// changes.
func (m *model) openReview(list []manager.Checked) tea.Cmd {
	b := &batchReview{items: make([]reviewItem, len(list))}
	version := m.herdrVersion
	cmds := make([]tea.Cmd, len(list))
	for i, ch := range list {
		ch.Plugin = m.current(ch.Plugin)
		b.items[i] = reviewItem{checked: ch}
		cmds[i] = func() tea.Msg { return reviewMsg{b: b, i: i, review: m.b.Review(m.ctx, ch, version)} }
	}
	m.review, m.screen = b, screenReview
	return m.withSpinner(tea.Batch(cmds...))
}

func onReview(msg reviewMsg) {
	r := msg.review
	msg.b.items[msg.i].review = &r
}

func (m *model) keyReview(a action) (tea.Model, tea.Cmd) {
	b := m.review
	switch a {
	case actUp, actDown, actPageUp, actPageDown, actTop, actBottom:
		m.moveReview(a)
	case actToggle:
		it := &b.items[b.cursor]
		switch {
		case it.review == nil:
		case !it.review.Ready():
			m.setStatus(it.checked.Plugin.PluginID+" cannot be updated: "+it.review.Blocker(), true)
		default:
			it.leftOut = !it.leftOut
		}
	case actOpen:
		m.openReviewed(b.items[b.cursor])
	case actUpdate, actUpdateAll:
		m.askApplyReview()
	case actClose, actQuit:
		m.screen, m.review = screenList, nil
	default:
	}
	return m, nil
}

func (m *model) moveReview(a action) {
	b := m.review
	page := m.pageSize()
	c := b.cursor
	switch a {
	case actUp:
		c--
	case actDown:
		c++
	case actPageUp:
		c -= page
	case actPageDown:
		c += page
	case actTop:
		c = 0
	case actBottom:
		c = len(b.items) - 1
	default:
	}
	b.cursor = min(max(c, 0), len(b.items)-1)
	if b.cursor < b.offset {
		b.offset = b.cursor
	}
	if b.cursor >= b.offset+page {
		b.offset = b.cursor - page + 1
	}
}

// openReviewed shows one reviewed update in full; back returns to the
// review.
func (m *model) openReviewed(it reviewItem) {
	r := it.review
	if r == nil {
		return
	}
	c := updateChange(it.checked)
	d := &detail{crumb: "Review", title: "Update " + it.checked.Plugin.Name, change: &c, review: m.review}
	if r.Err != nil {
		d.err = r.Err
	} else {
		d.preview, d.explain = r.Preview, &r.Explain
	}
	m.detail, m.screen = d, screenDetail
}

// askApplyReview asks before applying the included updates.
func (m *model) askApplyReview() {
	b := m.review
	list := b.includedItems()
	switch {
	case b.loading() > 0:
		m.setStatus("Still reading the updates", false)
		return
	case len(list) == 0:
		m.setStatus("No update is included", false)
		return
	case !m.idle():
		return
	}
	m.confirm = &confirm{
		prompt: fmt.Sprintf("Update %s? Their build commands run again", plural(len(list), "plugin")),
		run:    func() tea.Cmd { return m.applyReview(b) },
	}
}

func (m *model) viewReview() string {
	b := m.review
	title := "Review " + plural(len(b.items), "update")
	return m.frame(m.crumbs(tabNames[tabInstalled], title), nil, m.reviewItems())
}

// reviewItems are the updates: the plugin and whether it is included, then
// what the update is.
func (m *model) reviewItems() []string {
	t := m.theme
	b := m.review
	var out []string
	end := min(b.offset+m.pageSize(), len(b.items))
	for i := b.offset; i < end; i++ {
		it := b.items[i]
		selected := i == b.cursor
		nameStyle, descStyle := m.itemStyles(selected)
		p := it.checked.Plugin
		var state, desc string
		switch r := it.review; {
		case r == nil:
			state, desc = t.faint.Render("reading…"), descStyle.Render(it.checked.Result.Describe())
		case !r.Ready():
			state, desc = mark(t.err, glyphFailed, "cannot update"), t.err.Render(safe.Line(r.Blocker()))
		default:
			state = mark(t.ok, glyphEnabled, "included")
			if it.leftOut {
				state = mark(t.faint, glyphDisabled, "left out")
			}
			desc = descStyle.Render(safe.Line(r.Explain.Headline))
			if len(r.Explain.Runs) > 0 {
				desc += t.warn.Render(" · what it runs changes")
			}
		}
		title := strings.Join([]string{nameStyle.Render(p.Name), t.faint.Render(p.Version), state}, "  ")
		out = append(out, m.item(title, desc, selected)...)
	}
	return out
}

// reviewStatus is the status line of the review.
func (m *model) reviewStatus() string {
	t := m.theme
	b := m.review
	if n := b.loading(); n > 0 {
		return " " + m.spinner.View() + " " + t.fg2.Render(fmt.Sprintf("Reading %d of %d updates…", len(b.items)-n, len(b.items)))
	}
	n := len(b.includedItems())
	if n == 0 {
		return " " + t.faint.Render("No update is included"+m.keyHint(actToggle, "includes one"))
	}
	return " " + t.text.Render(fmt.Sprintf("%d of %d included", n, len(b.items))) + t.faint.Render(m.keyHint(actUpdate, "updates them"))
}
