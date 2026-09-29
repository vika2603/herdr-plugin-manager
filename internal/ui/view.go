package ui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-plugin-manager/internal/compat"
	"github.com/vika2603/herdr-plugin-manager/internal/manager"
	"github.com/vika2603/herdr-plugin-manager/internal/market"
	"github.com/vika2603/herdr-plugin-manager/internal/safe"
	"github.com/vika2603/herdr-plugin-manager/internal/source"
	"github.com/vika2603/herdr-plugin-manager/internal/updates"
)

// Lines around the body besides the help. The list has the tab bar, its
// underline and the tab's intro, a gap, the filter line, a gap and the status
// line; the output screen has a breadcrumb, a rule, a gap and the status
// line.
const (
	listChrome = 7
	// A plugin's screen has the breadcrumb, a gap, the view tabs and their
	// underline, a gap and the status line.
	detailChrome = 6
	otherChrome  = 4
	// itemHeight is a list item's two lines and the gap below them.
	itemHeight = 3
)

func (m *model) View() tea.View {
	var content string
	switch m.screen {
	case screenDetail:
		content = m.viewDetail()
	case screenOutput:
		content = m.viewOutput()
	case screenReview:
		content = m.viewReview()
	case screenHistory:
		content = m.viewHistory()
	case screenDoctor:
		content = m.viewDoctor()
	default:
		content = m.viewList()
	}
	v := tea.NewView(content)
	v.AltScreen = m.opts.AltScreen
	// Holding shift, or option in some terminals, still selects text.
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m *model) w() int {
	if m.width <= 0 {
		return 80
	}
	return m.width
}

func (m *model) h() int {
	if m.height <= 0 {
		return 24
	}
	return m.height
}

// bodyHeight is the number of lines between the chrome on the current
// screen.
func (m *model) bodyHeight() int {
	chrome := otherChrome
	switch m.screen {
	case screenList:
		chrome = listChrome
	case screenDetail:
		chrome = detailChrome
		if m.detail != nil && m.detail.versions != nil {
			chrome = otherChrome
		}
	case screenOutput, screenReview, screenHistory, screenDoctor:
	}
	return max(m.h()-chrome-len(m.helpLines()), 1)
}

// pageSize is the number of list items that fit.
func (m *model) pageSize() int {
	return max(m.bodyHeight()/itemHeight, 1)
}

// tabNames label the tabs and the breadcrumbs of screens opened from them.
var tabNames = [...]string{tabInstalled: "Installed", tabBrowse: "Marketplace"}

// tabIntro says what a tab holds, for someone opening it the first time.
func (m *model) tabIntro(t tab) string {
	if t == tabBrowse {
		return "Community plugins indexed by herdr.dev, not reviewed. Preview one to see what it runs."
	}
	return "Plugins installed or linked in herdr." + m.press(actSwitch, "for the marketplace")
}

// press is a sentence telling which key does a, or "" when a has no key.
func (m *model) press(a action, what string) string {
	if k := m.keys.name(a); k != "" {
		return " Press " + k + " " + what + "."
	}
	return ""
}

// keyHint is " · <key> <what>" for the status line, or "" when a has no key.
func (m *model) keyHint(a action, what string) string {
	if k := m.keys.name(a); k != "" {
		return " · " + k + " " + what
	}
	return ""
}

func (m *model) helpLines() []string {
	k := m.keyMap()
	if m.showHelp && m.confirm == nil && (m.screen != screenList || !m.filters[m.tab].Focused()) {
		return strings.Split(m.help.FullHelpView(k.FullHelp()), "\n")
	}
	return []string{m.shortHelp(k.ShortHelp())}
}

// shortHelp draws key chips with their descriptions, the first one, the
// screen's main action, highlighted. A last binding for ? always stays, so
// whatever does not fit is still one key away; others that do not fit are
// dropped from the end.
func (m *model) shortHelp(bindings []key.Binding) string {
	t := m.theme
	bindings = slices.DeleteFunc(slices.Clone(bindings), func(b key.Binding) bool { return !b.Enabled() })
	render := func(i int, b key.Binding) string {
		chip := t.keyChip
		if i == 0 {
			chip = t.keyChipMain
		}
		h := b.Help()
		return chip.Render(h.Key) + " " + t.keyDesc.Render(h.Desc)
	}
	const sep = "   "
	var tail string
	if n := len(bindings); n > 1 && bindings[n-1].Help().Key == "?" {
		tail = render(n-1, bindings[n-1])
		bindings = bindings[:n-1]
	}
	budget := m.w() - 2
	if tail != "" {
		budget -= ansi.StringWidth(tail) + len(sep)
	}
	var parts []string
	width := 0
	for i, b := range bindings {
		part := render(i, b)
		w := ansi.StringWidth(part) + len(sep)
		if width+w > budget {
			break
		}
		width += w
		parts = append(parts, part)
	}
	if tail != "" {
		parts = append(parts, tail)
	}
	return strings.Join(parts, sep)
}

func (m *model) frame(top []string, filter *string, body []string) string {
	lines := make([]string, 0, m.h())
	lines = append(lines, top...)
	lines = append(lines, "")
	if filter != nil {
		lines = append(lines, *filter, "")
	}
	height := m.bodyHeight()
	if m.confirm != nil {
		body = strings.Split(m.dialog(height), "\n")
	}
	for i := range height {
		if i < len(body) {
			lines = append(lines, body[i])
		} else {
			lines = append(lines, "")
		}
	}
	lines = append(lines, m.statusLine())
	for _, h := range m.helpLines() {
		lines = append(lines, " "+h)
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, m.w(), "…")
	}
	return strings.Join(lines, "\n")
}

func (m *model) dialog(height int) string {
	t := m.theme
	box := t.dialog.Render(t.bold.Render(m.confirm.prompt) + "\n\n" +
		t.keyChipMain.Render(m.keys.name(actConfirm)) + " " + t.keyDesc.Render("confirm") + "   " + t.faint.Render("any other key cancels"))
	return lipgloss.Place(m.w(), height, lipgloss.Center, lipgloss.Center, box)
}

// tabRow draws tab labels over an underline that runs the full width, thick
// and in the accent under the active tab. The list's tabs and a plugin's
// README and Info views use the same row, so a tab looks the same wherever
// it appears. A label's count is drawn after it when counts is not nil.
func (m *model) tabRow(names []string, counts []int, active int) (labels, underline string) {
	t := m.theme
	var l, u strings.Builder
	l.WriteString(" ")
	u.WriteString(t.rule.Render("─"))
	for i, name := range names {
		count := ""
		if counts != nil {
			count = strconv.Itoa(counts[i])
		}
		width := ansi.StringWidth(tabCell(names, counts, i))
		nameStyle, countStyle, line, glyph := t.tabInactive, t.tabCount, t.rule, "─"
		if i == active {
			nameStyle, countStyle, line, glyph = t.tabActive, t.accent, t.accent, "━"
		}
		l.WriteString(" " + nameStyle.Render(name) + " ")
		if count != "" {
			l.WriteString(countStyle.Render(count) + " ")
		}
		u.WriteString(line.Render(strings.Repeat(glyph, width)))
		l.WriteString("  ")
		u.WriteString(t.rule.Render("──"))
	}
	if rest := m.w() - ansi.StringWidth(u.String()); rest > 0 {
		u.WriteString(t.rule.Render(strings.Repeat("─", rest)))
	}
	return l.String(), u.String()
}

// tabBar is the list's header: its tabs, and what the current one holds.
func (m *model) tabBar() []string {
	labels, underline := m.tabRow(tabNames[:], []int{len(m.installed), len(m.entries)}, int(m.tab))
	return []string{
		spread(labels, m.versionLabel(), m.w()),
		underline,
		indent + m.theme.fg2.Render(m.tabIntro(m.tab)),
	}
}

// crumbs is the header of a screen opened from a tab: where it came from, and
// what it shows.
func (m *model) crumbs(from, title string) []string {
	t := m.theme
	left := indent + t.faint.Render(from+" ›") + " " + t.bold.Render(title)
	return []string{spread(left, m.versionLabel(), m.w()), t.rule.Render(strings.Repeat("─", m.w()))}
}

func (m *model) versionLabel() string {
	if m.herdrVersion == "" {
		return m.theme.faint.Render("herdr ? ")
	}
	return m.theme.faint.Render("herdr " + m.herdrVersion + " ")
}

// spread places left and right on one line of width, dropping right when
// there is no room.
func spread(left, right string, width int) string {
	gap := width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// statusLine is what is going on: a message just set, marked done or failed,
// work in progress with the spinner, or else a summary of the tab.
func (m *model) statusLine() string {
	t := m.theme
	switch {
	case m.busy != "" && (m.status == "" || !m.statusErr):
		return " " + m.spinner.View() + " " + m.busyLine()
	case m.status != "" && m.statusErr:
		return " " + t.err.Render(glyphFailed+" "+m.status)
	case m.status != "":
		return " " + t.ok.Render(glyphDone) + " " + t.text.Render(m.status)
	}
	progress := func(text string) string { return " " + m.spinner.View() + " " + t.fg2.Render(text) }
	switch m.screen {
	case screenReview:
		return m.reviewStatus()
	case screenHistory:
		return m.historyStatus()
	case screenDoctor:
		return m.doctorStatus()
	case screenList, screenDetail, screenOutput:
	}
	if m.screen != screenList {
		if m.spinning() {
			return progress("Reading the manifest from GitHub…")
		}
		return ""
	}
	if _, _, err := m.filterOf(m.tab); err != nil {
		return " " + t.err.Render(glyphFailed+" "+err.Error())
	}
	if m.tab == tabInstalled {
		switch {
		case m.installedErr != nil:
			return " " + t.err.Render(glyphFailed+" "+oneLine(m.installedErr.Error()))
		case !m.loaded:
			return progress("Loading plugins…")
		case m.checking:
			return progress("Checking for updates…")
		}
		n := len(m.available())
		failed, unchecked := m.checkGaps()
		var summary string
		switch n {
		case 0:
		case 1:
			summary = t.warn.Render(glyphUpdate+" 1 update") + t.faint.Render(m.keyHint(actUpdate, "to review"))
		default:
			summary = t.warn.Render(fmt.Sprintf("%s %d updates", glyphUpdate, n)) + t.faint.Render(m.keyHint(actUpdateAll, "updates all"))
		}
		failure := t.err.Render(glyphFailed + " " + plural(failed, "check") + " failed")
		switch {
		case failed > 0 && n > 0:
			return " " + summary + t.faint.Render(" · ") + failure
		case failed > 0:
			return " " + failure + t.faint.Render(m.keyHint(actCheck, "to retry"))
		case n > 0:
			return " " + summary
		case unchecked > 0:
			return " " + t.faint.Render("Updates not checked"+m.keyHint(actCheck, "to check"))
		}
		return " " + t.faint.Render("All GitHub plugins are up to date")
	}
	switch {
	case m.indexLoading:
		return progress("Loading the marketplace index…")
	case m.indexErr != nil:
		return " " + t.err.Render(glyphFailed+" "+oneLine(m.indexErr.Error()))
	}
	return " " + t.faint.Render("Index from "+m.indexStatus.FetchedAt.Format(time.DateTime)+" · listings are not reviewed by herdr")
}

func (m *model) viewList() string {
	t := m.theme
	f := m.filters[m.tab]
	var count string
	if m.tab == tabInstalled {
		count = counted(len(m.visibleInstalled()), len(m.installed), "plugin")
	} else {
		order := m.shownOrder().String()
		count = counted(len(m.visibleEntries()), len(m.entries), "plugin")
		if m.filters[tabBrowse].Value() != "" {
			count = fmt.Sprintf("%d of %d match", len(m.visibleEntries()), len(m.entries))
		}
		count += " · by " + order
	}
	right := t.faint.Render(count + " ")
	f.SetWidth(max(m.w()-ansi.StringWidth(right)-6, 10))
	left := indent + f.View()
	switch {
	case !f.Focused() && f.Value() == "":
		left = indent + t.accent.Render(f.Prompt) + t.faint.Render(f.Placeholder)
	case !f.Focused():
		// The search is kept while browsing its results; say how to change
		// or drop it.
		left = indent + t.accent.Render(f.Prompt) + t.text.Render(f.Value()) + t.faint.Render(m.keyHint(actSearch, "edits")+m.keyHint(actClose, "clears"))
	}
	// The count and order stay; a hint too long for the rest is cut short.
	left = ansi.Truncate(left, max(m.w()-ansi.StringWidth(right)-1, 0), "…")
	filter := spread(left, right, m.w())

	var body []string
	if m.tab == tabInstalled {
		body = m.installedItems()
	} else {
		body = m.browseBody()
	}
	return m.frame(m.tabBar(), &filter, body)
}

func counted(shown, total int, noun string) string {
	if shown == total {
		return plural(total, noun)
	}
	return fmt.Sprintf("%d of %d", shown, total)
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// item renders one two-line list item and the gap below it. Column 2 holds
// the bar that marks the selected item; the item starts in column 4.
func (m *model) item(title, desc string, selected bool) []string {
	bar := "  "
	if selected {
		bar = m.theme.selBar.Render(glyphSelected) + " "
	}
	return []string{" " + bar + title, " " + bar + desc, ""}
}

func (m *model) empty(text string) []string {
	return []string{"", indent + " " + m.theme.faint.Render(text)}
}

// itemStyles are the title and description styles of a list item.
func (m *model) itemStyles(selected bool) (title, desc lipgloss.Style) {
	if selected {
		return m.theme.bold, m.theme.soft
	}
	return m.theme.text, m.theme.fg2
}

func (m *model) installedItems() []string {
	t := m.theme
	list := m.visibleInstalled()
	if len(list) == 0 {
		switch {
		case !m.loaded || m.installedErr != nil:
			return nil
		case m.filters[tabInstalled].Value() != "":
			return m.empty("No installed plugin matches.")
		}
		return m.empty("No plugins installed yet." + m.press(actSwitch, "to browse the marketplace"))
	}
	_, text, _ := m.filterOf(tabInstalled)
	terms := market.Terms(text)
	var out []string
	start, end := m.offset[tabInstalled], min(m.offset[tabInstalled]+m.pageSize(), len(list))
	for i := start; i < end; i++ {
		p := list[i]
		selected := i == m.cursor[tabInstalled]
		nameStyle, descStyle := m.itemStyles(selected)
		title := strings.Join(append([]string{m.highlight(p.Name, terms, nameStyle), t.faint.Render(p.Version)}, m.installedMarks(p)...), "  ")
		desc := m.highlight(p.PluginID+" · "+manager.SourceLabel(p), terms, descStyle)
		out = append(out, m.item(title, desc, selected)...)
	}
	return out
}

// installedMarks are the states of an installed plugin worth a mark in the
// list; enabled, the usual state, is left unmarked there.
func (m *model) installedMarks(p herdr.InstalledPluginInfo) []string {
	t := m.theme
	var out []string
	if !p.Enabled {
		out = append(out, mark(t.faint, glyphDisabled, "disabled"))
	}
	if _, github := source.FromInstalled(p); !github {
		out = append(out, mark(t.fg2, glyphLocal, "local"))
	}
	if w := p.Warnings.ValueOrZero(); len(w) > 0 {
		out = append(out, mark(t.warn, glyphWarning, plural(len(w), "warning")))
	}
	ch, ok := m.checks[p.PluginID]
	switch {
	case !ok:
	case ch.Err != nil:
		out = append(out, mark(t.err, glyphFailed, "check failed"))
	case ch.Result.Kind == updates.Available:
		out = append(out, mark(t.warn, glyphUpdate, updateTarget(ch.Result)))
	case ch.Result.Kind == updates.Pinned:
		out = append(out, t.faint.Render("pinned"))
	}
	return out
}

// showsAll reports whether every term appears in text.
func showsAll(text string, terms []string) bool {
	text = strings.ToLower(text)
	for _, term := range terms {
		if !strings.Contains(text, term) {
			return false
		}
	}
	return true
}

// highlight renders text in base with every occurrence of a term in the
// accent. Text whose lower-case form changes length is drawn unmarked, since
// the match positions would not line up.
func (m *model) highlight(text string, terms []string, base lipgloss.Style) string {
	lower := strings.ToLower(text)
	if len(terms) == 0 || len(lower) != len(text) {
		return base.Render(text)
	}
	marked := make([]bool, len(text))
	for _, term := range terms {
		for i := 0; ; {
			j := strings.Index(lower[i:], term)
			if j < 0 {
				break
			}
			for k := i + j; k < i+j+len(term); k++ {
				marked[k] = true
			}
			i += j + len(term)
		}
	}
	match := m.theme.match
	var out strings.Builder
	for start := 0; start < len(text); {
		end := start
		for end < len(text) && marked[end] == marked[start] {
			end++
		}
		if marked[start] {
			out.WriteString(match.Render(text[start:end]))
		} else {
			out.WriteString(base.Render(text[start:end]))
		}
		start = end
	}
	return out.String()
}

// updateTarget names what an update moves to in the space of a mark: a new
// release, or new commits on the branch the plugin follows.
func updateTarget(r updates.Result) string {
	if r.TargetRef != "" && r.TargetRef != r.CurrentRef {
		return r.TargetRef
	}
	return "new commits"
}

func (m *model) viewDetail() string {
	d := m.detail
	var lines []string
	if d.view == viewReadme {
		lines = m.readmeLines(d)
	} else {
		lines = m.detailLines(d)
	}
	offset := &d.offsets[d.view]
	*offset = min(*offset, max(len(lines)-m.bodyHeight(), 0))
	return m.frame(m.detailHeader(d), nil, lines[*offset:])
}

// detailHeader is the breadcrumb on a line of its own, then the README and
// Info views as tabs. The version picker, which is neither, has the
// breadcrumb and a rule of the other screens.
func (m *model) detailHeader(d *detail) []string {
	t := m.theme
	if d.versions != nil {
		return m.crumbs(d.crumb+" › "+d.title, "Versions")
	}
	crumb := indent + t.faint.Render(d.crumb+" ›") + " " + t.bold.Render(d.title)
	labels, underline := m.tabRow(d.views(), nil, int(d.view))
	return []string{spread(crumb, m.versionLabel(), m.w()), "", labels, underline}
}

// detailLines renders the Info view.
func (m *model) detailLines(d *detail) []string {
	switch {
	case d.versions != nil:
		return m.versionLines(d)
	case d.plugin != nil:
		return m.installedDetail(d)
	case d.loading && d.entry != nil:
		return m.entryLines(d.entry)
	case d.loading:
		return nil
	case d.err != nil:
		return wrapIndented(indent+m.theme.err.Render(glyphFailed+" "+safe.Line(d.err.Error())), m.w()-1)
	}
	c := d.change
	lines := m.previewLines(d.preview, c != nil, d.entry)
	if c == nil {
		return lines
	}
	head := m.changeLines(d)
	if c.note != "" {
		head = append(append(wrapIndented(indent+m.theme.warn.Render(safe.Line(c.note)), m.w()-1), ""), head...)
	}
	return append(head, lines...)
}

// changeLines say what a change does, before its full preview: in words,
// from and to which version, what the authors wrote about it, and what
// changes in what the plugin runs.
func (m *model) changeLines(d *detail) []string {
	t := m.theme
	e := d.explain
	if e == nil {
		return []string{indent + t.faint.Render("Reading what changed…"), ""}
	}
	w := m.w() - 1
	out := wrapIndented(indent+t.bold.Render(safe.Line(e.Headline)), w)
	out = append(out, "")
	out = append(out, m.fields([]field{{"from", t.fg2.Render(safe.Line(e.From))}, {"to", t.text.Render(safe.Line(e.To))}})...)
	switch {
	case len(e.Releases) > 0:
		out = append(out, m.releaseNotes(d)...)
	case e.Commits != nil:
		out = append(out, "", m.heading("Commits", e.CommitsNote(), t.faint))
		for _, l := range e.CommitLines(15) {
			out = append(out, wrapIndented(subIndent+t.text.Render(l), w)...)
		}
	case e.NotesErr != nil:
		out = append(out, "", m.heading("What changed", "", t.faint))
		out = append(out, wrapIndented(subIndent+t.warn.Render(glyphWarning+" Not known: "+oneLine(e.NotesErr.Error())), w)...)
	}
	out = append(out, "", m.heading("Manifest changes", "", t.faint))
	if len(e.Runs) == 0 {
		out = append(out, subIndent+t.faint.Render("No change to what it runs or needs"))
	}
	for _, r := range e.Runs {
		style := t.text
		if strings.HasPrefix(r, "+ ") {
			style = t.warn
		}
		out = append(out, wrapIndented(subIndent+style.Render(r), w)...)
	}
	return append(out, "", m.heading("Manifest", "", t.faint))
}

// releaseNotes renders the notes of the releases a change brings in,
// keeping them for the width they were rendered at.
func (m *model) releaseNotes(d *detail) []string {
	width := max(m.w()-6, 20)
	if d.notes != nil && d.notesWidth == width {
		return d.notes
	}
	t := m.theme
	var out []string
	for _, r := range d.explain.Releases {
		title := "Release " + r.Tag
		if r.Name != "" && r.Name != r.Tag {
			title += " · " + r.Name
		}
		note := ""
		if !r.PublishedAt.IsZero() {
			note = r.PublishedAt.Format("2006-01-02")
		}
		out = append(out, "", m.heading(title, note, t.faint))
		if strings.TrimSpace(r.Notes) == "" {
			out = append(out, subIndent+t.faint.Render("This release has no notes."))
			continue
		}
		out = append(out, m.render(r.Notes, width)...)
	}
	d.notes, d.notesWidth = out, width
	return out
}

// wrapIndented wraps line to width, continuing wrapped lines at the line's
// own indentation.
func wrapIndented(line string, width int) []string {
	body := strings.TrimLeft(line, " ")
	pad := line[:len(line)-len(body)]
	wrapped := strings.Split(ansi.Wrap(body, max(width-len(pad), 10), ""), "\n")
	for i := range wrapped {
		wrapped[i] = pad + wrapped[i]
	}
	return wrapped
}

// runsOn is the platforms and herdr version a plugin needs, checked when
// nothing stands in the way.
func (m *model) runsOn(platforms []string, minHerdr string, fits bool) string {
	t := m.theme
	value := "any platform"
	if len(platforms) > 0 {
		value = strings.Join(platforms, ", ")
	}
	if minHerdr != "" {
		value += t.faint.Render(" · herdr ≥ " + minHerdr)
	}
	if fits {
		value += " " + t.ok.Render(glyphDone)
	}
	return value
}

// problems are reasons a plugin cannot be installed here.
func (m *model) problems(list []string) []string {
	if len(list) == 0 {
		return nil
	}
	t := m.theme
	out := []string{"", m.heading("Problems", "cannot be installed here", t.err)}
	for _, p := range list {
		out = append(out, wrapIndented(subIndent+t.err.Render(glyphFailed+" "+safe.Line(p)), m.w()-1)...)
	}
	return out
}

// entryLines is what the marketplace index says about a plugin, shown until
// the preview has read the manifest at the commit it would install.
func (m *model) entryLines(e *market.Entry) []string {
	t := m.theme
	mf := e.Manifest
	problems := compat.Problems(mf.Platforms, mf.MinHerdrVersion, m.herdrVersion, m.platform)
	out := []string{m.titleLine(mf.Name, mf.Version, mf.ID)}
	if mf.Description != "" {
		out = append(out, wrapIndented(indent+t.fg2.Render(mf.Description), m.w()-1)...)
	}
	out = append(out, "")
	out = append(out, m.fields(append(m.entryFields(*e),
		field{"source", t.text.Render(e.Source.String())}, field{"link", t.fg2.Render(e.Source.WebURL())}))...)
	out = append(out, m.problems(problems)...)
	out = append(out, "")
	out = append(out, m.fields(m.repoFields(*e, false))...)
	return append(out, "", indent+t.faint.Render("Reading what it runs from the manifest…"))
}

// previewLines lay out an install or update preview: what the plugin is,
// where it comes from, then what it runs. A change's preview leaves the
// commit and what it replaces to the lines above it. An install from the
// marketplace adds what the listing says about the repository.
func (m *model) previewLines(p *manager.Preview, change bool, e *market.Entry) []string {
	t := m.theme
	mf := p.Manifest
	out := []string{m.titleLine(safe.Line(mf.Name), safe.Line(mf.Version), safe.Line(mf.ID))}
	desc := mf.Description
	if desc == "" && e != nil {
		// The listing fell back to the repository's description.
		desc = e.Description()
	}
	if desc != "" {
		out = append(out, wrapIndented(indent+t.fg2.Render(safe.Line(desc)), m.w()-1)...)
	}
	out = append(out, "")

	ref := p.Ref
	if ref == "" {
		ref = "default branch"
		if p.DefaultBranch != "" {
			ref += " (" + p.DefaultBranch + ")"
		}
	}
	commit := p.Commit
	if len(commit) > 7 {
		commit = commit[:7]
	}
	platforms := make([]string, len(mf.Platforms))
	for i, pl := range mf.Platforms {
		platforms[i] = string(pl)
	}
	rows := []field{
		{"source", t.text.Render(p.Source.String()) + t.faint.Render(" @ ") + t.text.Render(ref)},
		{"link", t.fg2.Render(p.Source.WebURL())},
		{"runs on", m.runsOn(platforms, safe.Line(mf.MinHerdrVersion), len(p.Problems) == 0)},
	}
	if len(p.Releases) > 0 {
		rows = append(rows, field{"releases", m.releasesValue(p.Releases)})
	}
	if !change {
		rows[0].value += " " + t.faint.Render(commit)
	}
	if p.Existing != nil && !change {
		rows = append(rows, field{"replaces", t.text.Render(p.Existing.Version) + t.faint.Render(" from "+manager.SourceLabel(*p.Existing))})
	}
	rows = append(rows, field{"updates", t.fg2.Render(safe.Line(manager.TrackingAt(p.Ref, p.Commit).Describe()))})
	if e != nil {
		for _, f := range m.entryFields(*e) {
			if f.label == "topics" {
				rows = append(rows, f)
			}
		}
	}
	if e != nil {
		rows = append(append(rows, field{}), m.repoFields(*e, false)...)
	}
	out = append(out, m.fields(rows)...)
	out = append(out, m.problems(p.Problems)...)
	if len(p.Warnings) > 0 {
		out = append(out, "")
		out = append(out, m.section("Manifest warnings", "", t.warn, t.warn, printableAll(p.Warnings))...)
	}
	entries, hidden := p.Entrypoints()
	out = append(out, m.sections(entries)...)
	if hidden > 0 {
		out = append(out, "", indent+t.faint.Render(plural(hidden, "entry")+" for other platforms not shown"))
	}
	return out
}

// releasesValue lists the newest releases, the one install picks by default
// marked latest.
func (m *model) releasesValue(releases []string) string {
	t := m.theme
	const shown = 3
	parts := make([]string, 0, shown+1)
	for i, tag := range releases {
		if i == shown {
			parts = append(parts, t.faint.Render(fmt.Sprintf("+%d", len(releases)-shown)))
			break
		}
		part := t.text.Render(tag)
		if tag == latestRelease(releases) {
			part += " " + t.ok.Render("latest")
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, t.faint.Render(" · "))
}

// latestRelease is the newest release that is not a pre-release.
func latestRelease(releases []string) string {
	for _, tag := range releases {
		if !updates.IsPrerelease(tag) {
			return tag
		}
	}
	return ""
}

func printableAll(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = safe.Line(l)
	}
	return out
}

func (m *model) installedDetail(d *detail) []string {
	t := m.theme
	p := *d.plugin
	state := mark(t.ok, glyphEnabled, "enabled")
	if !p.Enabled {
		state = mark(t.faint, glyphDisabled, "disabled")
	}
	out := []string{m.titleLine(p.Name, p.Version, p.PluginID, state)}
	if desc := p.Description.ValueOrZero(); desc != "" {
		out = append(out, wrapIndented(indent+t.fg2.Render(desc), m.w()-1)...)
	}
	out = append(out, "")

	rows := []field{{"source", t.text.Render(manager.SourceLabel(p))}}
	if src, ok := p.Source.Get(); ok && src.ResolvedCommit.ValueOrZero() != "" {
		rows = append(rows, field{"commit", t.fg2.Render(src.ResolvedCommit.ValueOrZero())})
	}
	rows = append(rows, field{"root", t.fg2.Render(p.PluginRoot)})
	if v := p.MinHerdrVersion.ValueOrZero(); v != "" {
		rows = append(rows, field{"needs", t.text.Render("herdr ≥ " + v)})
	}
	rows = append(rows, field{"updates", t.fg2.Render(safe.Line(manager.TrackingOf(p).Describe())) + t.faint.Render(m.keyHint(actVersion, "for versions"))})
	if ch, ok := m.checks[p.PluginID]; ok {
		var value string
		switch {
		case ch.Err != nil:
			value = t.err.Render(glyphFailed + " " + safe.Line(ch.Err.Error()))
		case ch.Result.Kind == updates.Available:
			value = t.warn.Render(glyphUpdate+" "+ch.Result.Describe()) + t.faint.Render(m.keyHint(actUpdate, "to review"))
		default:
			value = t.fg2.Render(ch.Result.Describe())
		}
		rows = append(rows, field{"update", value})
	}
	out = append(out, m.fields(rows)...)
	if w := p.Warnings.ValueOrZero(); len(w) > 0 {
		out = append(out, "")
		out = append(out, m.section("Warnings", "", t.warn, t.warn, w)...)
	}

	var entry []string
	for _, a := range p.Actions.ValueOrZero() {
		entry = append(entry, fmt.Sprintf("action %s: %s", a.ID, a.Title))
	}
	for _, pn := range p.Panes.ValueOrZero() {
		entry = append(entry, fmt.Sprintf("pane %s: %s", pn.ID, pn.Title))
	}
	for _, e := range p.Events.ValueOrZero() {
		entry = append(entry, fmt.Sprintf("event %s: %s", e.On, manager.Command(e.Command)))
	}
	for _, s := range p.Startup.ValueOrZero() {
		entry = append(entry, "startup: "+manager.Command(s.Command))
	}
	if len(entry) > 0 {
		out = append(out, "")
		out = append(out, m.section("Entrypoints", "", t.faint, t.text, entry)...)
	}
	out = append(out, m.usageLines(d)...)
	return append(out, m.logLines(d)...)
}

// usageLines say how the plugin is used: its config directory, the command
// each action runs by and the keys bound to it, and a binding to add for an
// action no key runs.
func (m *model) usageLines(d *detail) []string {
	t := m.theme
	out := []string{"", m.heading("Use", "", t.faint)}
	u := d.usage
	if u == nil {
		return append(out, subIndent+t.faint.Render("reading…"))
	}
	config := t.fg2.Render(u.ConfigDir)
	if u.ConfigErr != nil {
		config = t.err.Render("not known: " + safe.Line(u.ConfigErr.Error()))
	}
	rows := []field{{"config", config}}
	var unbound *manager.UsageAction
	for i, a := range u.Actions {
		keys := t.faint.Render("no key bound")
		if len(a.Keys) > 0 {
			keys = t.ok.Render(strings.Join(a.Keys, ", "))
		} else if unbound == nil {
			unbound = &u.Actions[i]
		}
		rows = append(rows, field{"action", t.text.Render(safe.Line(a.Command)) + t.faint.Render(" · ") + keys})
	}
	if len(u.Actions) == 0 {
		rows = append(rows, field{"action", t.faint.Render("none, so no key can run it")})
	}
	if u.KeysErr != nil {
		rows = append(rows, field{"keys", t.err.Render("not known: " + safe.Line(u.KeysErr.Error()))})
	}
	out = append(out, m.fields(rows)...)
	if unbound != nil {
		out = append(out, "", m.heading("Bind a key", "in "+u.HerdrConfig+", then herdr server reload-config", t.faint))
		for _, l := range unbound.Snippet() {
			out = append(out, subIndent+t.fg2.Render(safe.Line(l)))
		}
	}
	return out
}

// logLines are the commands herdr ran for the plugin, the newest first.
func (m *model) logLines(d *detail) []string {
	t := m.theme
	out := []string{"", m.heading("Recent command logs", "", t.faint)}
	switch {
	case d.logsErr != nil:
		return append(out, subIndent+t.err.Render(glyphFailed+" "+safe.Line(d.logsErr.Error())))
	case d.logs == nil:
		return append(out, subIndent+t.faint.Render("loading…"))
	case len(d.logs) == 0:
		return append(out, subIndent+t.faint.Render("none"))
	}
	for _, l := range d.logs {
		glyph, style := t.ok.Render(glyphDone), t.text
		if l.Status == herdr.PluginCommandStatusFailed {
			glyph, style = t.err.Render(glyphFailed), t.err
		}
		out = append(out, subIndent+glyph+" "+style.Render(safe.Line(manager.LogHeader(l))))
		for _, stream := range []struct{ name, text string }{{"stdout", l.Stdout.ValueOrZero()}, {"stderr", l.Stderr.ValueOrZero()}} {
			text := strings.TrimRight(safe.Text(stream.text), "\n")
			if strings.TrimSpace(text) == "" {
				continue
			}
			out = append(out, subIndent+"  "+t.fg2.Render(stream.name+":"))
			for line := range strings.SplitSeq(text, "\n") {
				out = append(out, wrapIndented(subIndent+"    "+t.faint.Render(line), m.w()-1)...)
			}
		}
	}
	return out
}

// outputCache is the output screen's lines, wrapped to the width they are
// drawn at but not styled, so a long output is not wrapped again on every
// frame and a running operation only has its new lines wrapped.
type outputCache struct {
	// src is the output the lines are of: a running operation or a page.
	src   any
	width int
	// done counts the bytes of the output in lines; a running operation's
	// line not yet ended is left out.
	done  int
	lines []string
}

func (c *outputCache) add(text string) {
	c.lines = appendWrapped(c.lines, text, c.width)
}

// appendWrapped adds the lines of text to out, wrapping those wider than
// width.
func appendWrapped(out []string, text string, width int) []string {
	for line := range strings.SplitSeq(safe.Text(text), "\n") {
		// A line has no more columns than bytes.
		if len(line) <= width || ansi.StringWidth(line) <= width {
			out = append(out, line)
			continue
		}
		out = append(out, strings.Split(ansi.Wrap(line, width, ""), "\n")...)
	}
	return out
}

// outputPad indents the output screen's lines.
const outputPad = indent + " "

// viewOutput shows what herdr printed: as it prints it while an operation
// runs, else the page chosen, the last operation's or a recorded change's.
// Only the lines on screen are styled.
func (m *model) viewOutput() string {
	t := m.theme
	var (
		page output
		src  any
		more func(from int) string
	)
	switch {
	case m.live != nil:
		page, src, more = output{title: m.busy + " (running)"}, m.live, m.live.from
		if m.live.stopping {
			page.title = m.busy + " (cancelling)"
		}
	case m.page != nil:
		page, src = *m.page, m.page
	default:
		page, src = m.output, &m.output
	}
	if more == nil {
		// herdr's output includes what the plugin's build commands printed.
		text := strings.TrimRight(page.text, "\n")
		more = func(from int) string { return text[min(from, len(text)):] }
	}
	title := page.title
	switch {
	case page.cancelled:
		title += " cancelled"
	case page.err != nil:
		title += " failed"
	}

	c := &m.outputLines
	width := max(m.w()-1-len(outputPad), 10)
	if c.src != src || c.width != width {
		*c = outputCache{src: src, width: width}
	}
	chunk := more(c.done)
	if m.live != nil {
		// The last line may still be printed to; it is wrapped anew until it
		// ends.
		if i := strings.LastIndexByte(chunk, '\n'); i >= 0 {
			c.add(chunk[:i])
			c.done += i + 1
			chunk = chunk[i+1:]
		}
	} else if chunk != "" {
		c.add(chunk)
		c.done += len(chunk)
		chunk = ""
	}
	var rest []string
	if chunk != "" {
		rest = appendWrapped(nil, chunk, width)
	}
	if len(c.lines)+len(rest) == 0 {
		rest = []string{"(herdr printed nothing)"}
		if m.live != nil {
			rest = []string{"(herdr has printed nothing yet)"}
		}
	}
	var errLines []string
	if page.err != nil {
		for line := range strings.SplitSeq(safe.Text(page.err.Error()), "\n") {
			errLines = append(errLines, wrapIndented(outputPad+t.err.Render(line), m.w()-1)...)
		}
	}

	total := len(c.lines) + len(rest) + len(errLines)
	height := m.bodyHeight()
	m.outputMax = max(total-height, 0)
	if m.outputFollow {
		m.outputOffset = m.outputMax
	}
	m.outputOffset = min(m.outputOffset, m.outputMax)
	body := make([]string, 0, height)
	for i := m.outputOffset; i < total && len(body) < height; i++ {
		switch j := i - len(c.lines); {
		case j < 0:
			body = append(body, outputPad+t.text.Render(c.lines[i]))
		case j < len(rest):
			body = append(body, outputPad+t.text.Render(rest[j]))
		default:
			body = append(body, errLines[j-len(rest)])
		}
	}
	from := tabNames[m.tab]
	if page.back == screenHistory {
		from = "History"
	}
	return m.frame(m.crumbs(from, title), nil, body)
}
