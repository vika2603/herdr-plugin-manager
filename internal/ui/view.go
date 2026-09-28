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

// tabIntros say what each tab holds, for someone opening it the first time.
var tabIntros = [...]string{
	tabInstalled: "Plugins installed or linked in herdr. Press tab for the marketplace.",
	tabBrowse:    "Community plugins indexed by herdr.dev, not reviewed. Preview one to see what it runs.",
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
	box := t.dialog.Render(t.itemTitle.Bold(true).Render(m.confirm.prompt) + "\n\n" +
		t.faint.Render("y confirm · any other key cancels"))
	return lipgloss.Place(m.w(), height, lipgloss.Center, lipgloss.Center, box)
}

// tabRow draws tab labels over an underline that runs the full width, thick
// under the active tab. The list's tabs and a plugin's README and Info views
// use the same row, so a tab looks the same wherever it appears. A label's
// count is drawn after it when counts is not nil.
func (m *model) tabRow(names []string, counts []int, active int) (labels, underline string) {
	t := m.theme
	var l, u strings.Builder
	l.WriteString(" ")
	u.WriteString(t.rule.Render("─"))
	for i, name := range names {
		cell := " " + name + " "
		count := ""
		if counts != nil {
			count = strconv.Itoa(counts[i])
			cell += count + " "
		}
		width := ansi.StringWidth(cell)
		nameStyle, countStyle, line, glyph := t.tabInactive, t.tabCount, t.rule, "─"
		if i == active {
			nameStyle, countStyle, line, glyph = t.tabActive, t.tabActive.UnsetBold(), t.tabLine, "━"
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
		"  " + m.theme.intro.Render(tabIntros[m.tab]),
	}
}

// crumbs is the header of a screen opened from a tab: where it came from, and
// what it shows.
func (m *model) crumbs(from, title string) []string {
	t := m.theme
	left := "  " + t.crumb.Render(from+" ›") + " " + t.crumbTitle.Render(title)
	return []string{spread(left, m.versionLabel(), m.w()), t.rule.Render(strings.Repeat("─", m.w()))}
}

func (m *model) versionLabel() string {
	if m.herdrVersion == "" {
		return m.theme.version.Render("herdr ? ")
	}
	return m.theme.version.Render("herdr " + m.herdrVersion + " ")
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

func (m *model) statusLine() string {
	t := m.theme
	switch {
	case m.busy != "":
		return " " + m.spinner.View() + " " + m.busy + "…"
	case m.status != "":
		if m.statusErr {
			return " " + t.err.Render(m.status)
		}
		return " " + t.ok.Render(m.status)
	}
	if m.screen != screenList {
		if m.spinning() {
			return " " + m.spinner.View() + t.faint.Render(" Reading the manifest from GitHub…")
		}
		return ""
	}
	if m.tab == tabInstalled {
		switch {
		case m.installedErr != nil:
			return " " + t.err.Render(oneLine(m.installedErr.Error()))
		case !m.loaded:
			return " " + m.spinner.View() + t.faint.Render(" Loading plugins…")
		case m.checking:
			return " " + m.spinner.View() + t.faint.Render(" Checking for updates…")
		}
		switch n := len(m.available()); n {
		case 0:
			return " " + t.faint.Render("All GitHub plugins are up to date")
		case 1:
			return " " + t.warn.Render("1 update available") + t.faint.Render(" · u to review")
		default:
			return " " + t.warn.Render(fmt.Sprintf("%d updates available", n)) + t.faint.Render(" · U updates all")
		}
	}
	switch {
	case m.indexLoading:
		return " " + m.spinner.View() + t.faint.Render(" Loading the marketplace index…")
	case m.indexErr != nil:
		return " " + t.err.Render(oneLine(m.indexErr.Error()))
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
			order = market.ByStars.String()
		}
		count = counted(len(m.visibleEntries()), len(m.entries), "plugin") + " · by " + order
	}
	right := t.faint.Render(count + " ")
	f.SetWidth(max(m.w()-ansi.StringWidth(right)-6, 10))
	left := " " + f.View()
	if !f.Focused() && f.Value() == "" {
		left = " " + t.faint.Render("/ "+f.Placeholder)
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

// item renders one two-line list item and the gap below it. The selected
// item is marked by a bar on its left, as in Bubbles' list.
func (m *model) item(title, desc string, selected bool) []string {
	bar := "  "
	if selected {
		bar = m.theme.selBar.Render("│") + " "
	}
	return []string{" " + bar + title, " " + bar + desc, ""}
}

func (m *model) empty(text string) []string {
	return []string{"", "   " + m.theme.faint.Render(text)}
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
		return m.empty("No plugins installed yet. Press tab to browse the marketplace.")
	}
	terms := market.Terms(m.filters[tabInstalled].Value())
	var out []string
	start, end := m.offset[tabInstalled], min(m.offset[tabInstalled]+m.pageSize(), len(list))
	for i := start; i < end; i++ {
		p := list[i]
		selected := i == m.cursor[tabInstalled]
		nameStyle, descStyle := t.itemTitle, t.itemDesc
		if selected {
			nameStyle, descStyle = t.selTitle, t.selDesc
		}
		title := m.highlight(p.Name, terms, nameStyle) + " " + t.version.Render(p.Version)
		if badges := m.installedBadges(p); len(badges) > 0 {
			title += " " + strings.Join(badges, " ")
		}
		desc := m.highlight(p.PluginID+" · "+manager.SourceLabel(p), terms, descStyle)
		out = append(out, m.item(title, desc, selected)...)
	}
	return out
}

func (m *model) installedBadges(p herdr.InstalledPluginInfo) []string {
	t := m.theme
	var out []string
	if !p.Enabled {
		out = append(out, t.badgeDisabled.Render("disabled"))
	}
	if _, github := source.FromInstalled(p); !github {
		out = append(out, t.badgeLocal.Render("local"))
	}
	if w := p.Warnings.ValueOrZero(); len(w) > 0 {
		out = append(out, t.badgeWarn.Render(plural(len(w), "warning")))
	}
	ch, ok := m.checks[p.PluginID]
	switch {
	case !ok:
	case ch.Err != nil:
		out = append(out, t.badgeError.Render("check failed"))
	case ch.Result.Kind == updates.Available:
		out = append(out, t.badgeUpdate.Render("update → "+updateTarget(ch.Result)))
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

// highlight renders text in base with every occurrence of a term marked.
// Text whose lower-case form changes length is drawn unmarked, since the
// match positions would not line up.
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
	match := m.theme.match.Inherit(base)
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

// updateTarget names what an update moves to in the space of a badge.
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
		nameStyle, descStyle := t.itemTitle, t.itemDesc
		if selected {
			nameStyle, descStyle = t.selTitle, t.selDesc
		}
		name := e.Manifest.Name
		if name == "" {
			name = e.Manifest.ID
		}
		title := m.highlight(name, terms, nameStyle) + " " + t.version.Render(e.Manifest.Version) + "  " + t.star.Render(fmt.Sprintf("★ %d", e.Repo.Stars))
		if installed[e.Manifest.ID] {
			title += " " + t.badgeInstalled.Render("installed")
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
	crumb := "  " + t.crumb.Render(d.crumb+" ›") + " " + t.crumbTitle.Render(d.title)
	labels, underline := m.tabRow(d.views(), nil, int(d.view))
	return []string{spread(crumb, m.versionLabel(), m.w()), "", labels, underline}
}

// detailLines renders the detail content wrapped to the width.
func (m *model) detailLines(d *detail) []string {
	var raw []string
	switch {
	case d.plugin != nil:
		raw = m.installedDetail(d)
	case d.loading && d.entry != nil:
		raw = m.sectionLines(m.entrySections(d.entry))
	case d.loading:
	case d.err != nil:
		raw = []string{"   " + m.theme.err.Render(safe.Line(d.err.Error()))}
	default:
		raw = m.sectionLines(d.preview.Sections())
	}
	var out []string
	for _, line := range raw {
		out = append(out, wrapIndented(line, m.w()-1)...)
	}
	return out
}

// wrapIndented wraps line to width, continuing wrapped lines at the line's
// own indentation.
func wrapIndented(line string, width int) []string {
	body := strings.TrimLeft(line, " ")
	indent := line[:len(line)-len(body)]
	wrapped := strings.Split(ansi.Wrap(body, max(width-len(indent), 10), ""), "\n")
	for i := range wrapped {
		wrapped[i] = indent + wrapped[i]
	}
	return wrapped
}

// entrySections is what the marketplace index says about a plugin, shown
// until the preview has read the manifest at the commit it would install.
func (m *model) entrySections(e *market.Entry) []manager.Section {
	mf := e.Manifest
	platforms := "undeclared"
	if len(mf.Platforms) > 0 {
		platforms = strings.Join(mf.Platforms, ", ")
	}
	summary := []string{
		fmt.Sprintf("%s %s (%s)", mf.Name, mf.Version, mf.ID),
		"source: " + e.Source.String() + " @ default branch",
		"link: " + e.Source.WebURL(),
	}
	if mf.Description != "" {
		summary = append(summary, mf.Description)
	}
	summary = append(summary, "platforms: "+platforms, "min herdr: "+mf.MinHerdrVersion)
	out := []manager.Section{{Title: "Plugin", Lines: summary}}
	if problems := compat.Problems(mf.Platforms, mf.MinHerdrVersion, m.herdrVersion, compat.Platform()); len(problems) > 0 {
		out = append(out, manager.Section{Title: "Problems", Lines: problems})
	}
	return out
}

// sectionLines draws sections: a heading, then its lines in the style their
// content calls for. Everything but the summary sections is commands.
func (m *model) sectionLines(sections []manager.Section) []string {
	t := m.theme
	var out []string
	for i, s := range sections {
		if i > 0 {
			out = append(out, "")
		}
		heading, body := t.heading, t.itemTitle
		switch s.Title {
		case "Problems":
			heading, body = t.err.Bold(true), t.err
		case "Warnings", "Manifest warnings":
			heading, body = t.warn.Bold(true), t.warn
		case "Plugin", "Update":
		default:
			body = t.code
		}
		out = append(out, "  "+heading.Render(s.Title))
		for _, line := range s.Lines {
			out = append(out, "    "+body.Render(line))
		}
	}
	return out
}

func (m *model) installedDetail(d *detail) []string {
	t := m.theme
	p := *d.plugin
	state := t.badgeEnabled.Render("enabled")
	if !p.Enabled {
		state = t.badgeDisabled.Render("disabled")
	}
	out := []string{"  " + t.selTitle.Render(p.Name) + " " + t.version.Render(p.Version) + "  " + state}
	if desc := p.Description.ValueOrZero(); desc != "" {
		out = append(out, "  "+t.itemDesc.Render(desc))
	}
	out = append(out, "")

	info := []string{"id: " + p.PluginID, "source: " + manager.SourceLabel(p), "root: " + p.PluginRoot}
	if src, ok := p.Source.Get(); ok && src.ResolvedCommit.ValueOrZero() != "" {
		info = append(info, "commit: "+src.ResolvedCommit.ValueOrZero())
	}
	if v := p.MinHerdrVersion.ValueOrZero(); v != "" {
		info = append(info, "min herdr: "+v)
	}
	sections := []manager.Section{{Title: "Plugin", Lines: info}}

	if ch, ok := m.checks[p.PluginID]; ok {
		line := ch.Result.Describe()
		switch {
		case ch.Err != nil:
			line = t.err.Render(safe.Line(ch.Err.Error()))
		case ch.Result.Kind == updates.Available:
			line = t.warn.Render(line) + t.faint.Render(" · u to review the update")
		}
		sections = append(sections, manager.Section{Title: "Update", Lines: []string{line}})
	}
	if w := p.Warnings.ValueOrZero(); len(w) > 0 {
		sections = append(sections, manager.Section{Title: "Warnings", Lines: w})
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
		sections = append(sections, manager.Section{Title: "Entrypoints", Lines: entry})
	}
	out = append(out, m.sectionLines(sections)...)

	out = append(out, "", "  "+t.heading.Render("Recent command logs"))
	switch {
	case d.logsErr != nil:
		out = append(out, "    "+t.err.Render(safe.Line(d.logsErr.Error())))
	case d.logs == nil:
		out = append(out, "    "+t.faint.Render("loading…"))
	case len(d.logs) == 0:
		out = append(out, "    "+t.faint.Render("none"))
	}
	for _, l := range d.logs {
		style := t.code
		if l.Status == herdr.PluginCommandStatusFailed {
			style = t.err
		}
		out = append(out, "    "+style.Render(manager.LogHeader(l)))
		if text := strings.TrimSpace(l.Stderr.ValueOrZero()); text != "" {
			for line := range strings.SplitSeq(text, "\n") {
				out = append(out, "      "+t.faint.Render(line))
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
		lines = append(lines, wrapIndented("   "+t.code.Render(line), m.w()-1)...)
	}
	if m.output.err != nil {
		for line := range strings.SplitSeq(safe.Text(m.output.err.Error()), "\n") {
			lines = append(lines, wrapIndented("   "+t.err.Render(line), m.w()-1)...)
		}
	}
	m.outputOffset = min(m.outputOffset, max(len(lines)-m.bodyHeight(), 0))
	return m.frame(m.crumbs(tabNames[m.tab], title), nil, lines[m.outputOffset:])
}
