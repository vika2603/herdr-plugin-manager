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
	case screenOutput:
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
	if m.showHelp && m.screen == screenList && m.confirm == nil && !m.filters[m.tab].Focused() {
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
	case m.busy != "":
		return " " + m.spinner.View() + " " + t.text.Render(m.busy+"…")
	case m.status != "" && m.statusErr:
		return " " + t.err.Render(glyphFailed+" "+m.status)
	case m.status != "":
		return " " + t.ok.Render(glyphDone) + " " + t.text.Render(m.status)
	}
	progress := func(text string) string { return " " + m.spinner.View() + " " + t.fg2.Render(text) }
	if m.screen != screenList {
		if m.spinning() {
			return progress("Reading the manifest from GitHub…")
		}
		return ""
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
		order := m.order.String()
		if m.order == market.ByRelevance && m.filters[tabBrowse].Value() == "" {
			order = market.ByPopular.String()
		}
		count = counted(len(m.visibleEntries()), len(m.entries), "plugin") + " · by " + order
	}
	right := t.faint.Render(count + " ")
	f.SetWidth(max(m.w()-ansi.StringWidth(right)-6, 10))
	left := indent + f.View()
	if !f.Focused() && f.Value() == "" {
		left = indent + t.accent.Render(f.Prompt) + t.faint.Render(f.Placeholder)
	}
	filter := spread(left, right, m.w())

	var body []string
	if m.tab == tabInstalled {
		body = m.installedItems()
	} else {
		body = m.browseItems()
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
	terms := market.Terms(m.filters[tabInstalled].Value())
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

// updateTarget names what an update moves to in the space of a mark.
func updateTarget(r updates.Result) string {
	if r.TargetRef != "" && r.TargetRef != r.CurrentRef {
		return r.TargetRef
	}
	if len(r.TargetCommit) > 7 {
		return r.TargetCommit[:7]
	}
	return r.TargetCommit
}

func (m *model) browseItems() []string {
	t := m.theme
	entries := m.visibleEntries()
	if src, ok := m.typedSource(); ok {
		return m.empty("Not in the marketplace." + m.press(actOpen, "to preview "+src.String()+" from GitHub"))
	}
	if len(entries) == 0 {
		if m.indexLoading || m.indexErr != nil {
			return nil
		}
		return m.empty("No marketplace plugin matches.")
	}
	installed := m.installedIDs()
	terms := market.Terms(m.filters[tabBrowse].Value())
	var out []string
	start, end := m.offset[tabBrowse], min(m.offset[tabBrowse]+m.pageSize(), len(entries))
	for i := start; i < end; i++ {
		e := entries[i]
		selected := i == m.cursor[tabBrowse]
		nameStyle, descStyle := m.itemStyles(selected)
		name := e.Manifest.Name
		if name == "" {
			name = e.Manifest.ID
		}
		title := m.highlight(name, terms, nameStyle) + "  " + t.faint.Render(fmt.Sprintf("%s  %s %d", e.Manifest.Version, glyphStar, e.Repo.Stars))
		if installed[e.Manifest.ID] {
			title += "  " + mark(t.ok, glyphDone, "installed")
		}
		// The description line shows where the terms matched: the source,
		// the description that mentions them, and any matching topics.
		desc := e.Source.String()
		shown := market.ShownDescription(e, terms)
		// Topics are listed only for a term the visible text does not show.
		var topics string
		if matched := market.MatchedTopics(e, terms); len(matched) > 0 && !showsAll(name+" "+desc+" "+shown, terms) {
			topics = " · topics: " + strings.Join(matched, ", ")
		}
		if d := shown; d != "" {
			room := m.w() - 4 - ansi.StringWidth(desc+" · "+topics)
			desc += " · " + market.Snippet(d, terms, max(room, 20))
		}
		line := m.highlight(desc+topics, terms, descStyle)
		out = append(out, m.item(title, line, selected)...)
	}
	return out
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
// Info views as tabs.
func (m *model) detailHeader(d *detail) []string {
	t := m.theme
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
	lines := m.previewLines(d.preview)
	if c := d.change; c != nil && c.note != "" {
		note := wrapIndented(indent+m.theme.warn.Render(safe.Line(c.note)), m.w()-1)
		lines = append(append(note, ""), lines...)
	}
	return lines
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
	problems := compat.Problems(mf.Platforms, mf.MinHerdrVersion, m.herdrVersion, compat.Platform())
	out := []string{m.titleLine(mf.Name, mf.Version, mf.ID)}
	if mf.Description != "" {
		out = append(out, wrapIndented(indent+t.fg2.Render(mf.Description), m.w()-1)...)
	}
	out = append(out, "")
	out = append(out, m.fields([]field{
		{"source", t.text.Render(e.Source.String())},
		{"link", t.fg2.Render(e.Source.WebURL())},
		{"runs on", m.runsOn(mf.Platforms, mf.MinHerdrVersion, len(problems) == 0)},
	})...)
	out = append(out, m.problems(problems)...)
	return append(out, "", indent+t.faint.Render("Reading what it runs from the manifest…"))
}

// previewLines lay out an install or update preview: what the plugin is,
// where it comes from, then what it runs.
func (m *model) previewLines(p *manager.Preview) []string {
	t := m.theme
	mf := p.Manifest
	out := []string{m.titleLine(safe.Line(mf.Name), safe.Line(mf.Version), safe.Line(mf.ID))}
	if mf.Description != "" {
		out = append(out, wrapIndented(indent+t.fg2.Render(safe.Line(mf.Description)), m.w()-1)...)
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
		{"source", t.text.Render(p.Source.String()) + t.faint.Render(" @ ") + t.text.Render(ref) + " " + t.faint.Render(commit)},
		{"link", t.fg2.Render(p.Source.WebURL())},
		{"runs on", m.runsOn(platforms, safe.Line(mf.MinHerdrVersion), len(p.Problems) == 0)},
	}
	if len(p.Releases) > 0 {
		rows = append(rows, field{"releases", m.releasesValue(p.Releases)})
	}
	if p.Existing != nil {
		rows = append(rows, field{"replaces", t.text.Render(p.Existing.Version) + t.faint.Render(" from "+manager.SourceLabel(*p.Existing))})
	}
	rows = append(rows, field{"updates", t.fg2.Render(safe.Line(manager.TrackingAt(p.Ref, p.Commit).Describe()))})
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
	return append(out, m.logLines(d)...)
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
		out = append(out, subIndent+glyph+" "+style.Render(manager.LogHeader(l)))
		if text := strings.TrimSpace(l.Stderr.ValueOrZero()); text != "" {
			for line := range strings.SplitSeq(text, "\n") {
				out = append(out, subIndent+"  "+t.faint.Render(line))
			}
		}
	}
	return out
}

func (m *model) viewOutput() string {
	t := m.theme
	title := m.output.title
	if m.output.err != nil {
		title += " failed"
	}
	// herdr's output includes what the plugin's build commands printed.
	text := safe.Text(strings.TrimRight(m.output.text, "\n"))
	if text == "" {
		text = "(herdr printed nothing)"
	}
	var lines []string
	for line := range strings.SplitSeq(text, "\n") {
		lines = append(lines, wrapIndented(indent+" "+t.text.Render(line), m.w()-1)...)
	}
	if m.output.err != nil {
		for line := range strings.SplitSeq(safe.Text(m.output.err.Error()), "\n") {
			lines = append(lines, wrapIndented(indent+" "+t.err.Render(line), m.w()-1)...)
		}
	}
	m.outputOffset = min(m.outputOffset, max(len(lines)-m.bodyHeight(), 0))
	return m.frame(m.crumbs(tabNames[m.tab], title), nil, lines[m.outputOffset:])
}
