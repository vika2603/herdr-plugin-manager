package ui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-plugin-manager/internal/compat"
	"github.com/vika2603/herdr-plugin-manager/internal/manager"
	"github.com/vika2603/herdr-plugin-manager/internal/market"
	"github.com/vika2603/herdr-plugin-manager/internal/source"
	"github.com/vika2603/herdr-plugin-manager/internal/updates"
)

// wideBrowse is the narrowest screen that shows the selected plugin's
// listing beside the marketplace list.
const wideBrowse = 110

// browseColumns are the widths of the marketplace list and of the listing
// beside it, 0 when the screen is too narrow for one.
func (m *model) browseColumns() (list, pane int) {
	if m.w() < wideBrowse {
		return m.w(), 0
	}
	list = m.w() * 11 / 20
	return list, m.w() - list - 2
}

// browseBody is the marketplace list, and on a wide screen the selected
// plugin's listing beside it.
func (m *model) browseBody() []string {
	listWidth, paneWidth := m.browseColumns()
	left := m.browseItems(listWidth)
	entries := m.visibleEntries()
	c := m.cursor[tabBrowse]
	if paneWidth == 0 || c >= len(entries) {
		return left
	}
	right := m.entryPane(entries[c], paneWidth)
	if h := m.bodyHeight(); len(right) > h {
		// The rest is on the plugin's screen, which scrolls.
		right = append(right[:h-1], m.theme.faint.Render("…"+m.press(actOpen, "shows the rest")))
	}
	sep := m.theme.rule.Render("│") + " "
	out := make([]string, m.bodyHeight())
	for i := range out {
		var l, r string
		if i < len(left) {
			l = ansi.Truncate(left[i], listWidth-1, "…")
		}
		if i < len(right) {
			r = right[i]
		}
		out[i] = l + strings.Repeat(" ", max(listWidth-ansi.StringWidth(l), 0)) + sep + r
	}
	return out
}

// browseItems are the marketplace list items in a column width cells wide:
// the name, version, stars and state, then what the plugin is for with the
// repository's language and last push.
func (m *model) browseItems(width int) []string {
	entries := m.visibleEntries()
	if src, ok := m.typedSource(); ok {
		return m.empty("Not in the marketplace." + m.press(actOpen, "to preview "+src.String()+" from GitHub"))
	}
	if len(entries) == 0 {
		if m.indexLoading || m.indexErr != nil {
			return nil
		}
		return m.emptyBrowse()
	}
	terms := market.Terms(m.filters[tabBrowse].Value())
	var out []string
	start, end := m.offset[tabBrowse], min(m.offset[tabBrowse]+m.pageSize(), len(entries))
	for i := start; i < end; i++ {
		e := entries[i]
		selected := i == m.cursor[tabBrowse]
		nameStyle, descStyle := m.itemStyles(selected)
		title := m.entryTitle(e, terms, nameStyle, width-4)
		out = append(out, m.item(title, m.entryLine(e, terms, descStyle, width-4), selected)...)
	}
	return out
}

// entryTitle is the first line of a list item: the name, version and stars,
// then the marks. The marks always fit: the stars go first, then the version
// and the name are cut short.
func (m *model) entryTitle(e market.Entry, terms []string, nameStyle lipgloss.Style, width int) string {
	t := m.theme
	const gap = "  "
	marks := strings.Join(m.entryMarks(e), gap)
	room := width
	if marks != "" {
		room -= ansi.StringWidth(marks) + len(gap)
	}
	name, version, stars := entryName(e), e.Manifest.Version, fmt.Sprintf("%s %d", glyphStar, e.Repo.Stars)
	fits := func() bool {
		w := ansi.StringWidth(name) + len(gap) + ansi.StringWidth(version)
		if stars != "" {
			w += len(gap) + ansi.StringWidth(stars)
		}
		return w <= room
	}
	if !fits() {
		stars = ""
	}
	if !fits() {
		version = ansi.Truncate(version, max(room/4, 6), "…")
	}
	styledName := m.highlight(name, terms, nameStyle)
	if !fits() {
		name = ansi.Truncate(name, max(room-ansi.StringWidth(version)-len(gap), 4), "…")
		styledName = nameStyle.Render(name)
	}
	parts := []string{styledName, t.faint.Render(version)}
	if stars != "" {
		parts = append(parts, t.faint.Render(stars))
	}
	if marks != "" {
		parts = append(parts, marks)
	}
	return strings.Join(parts, gap)
}

func entryName(e market.Entry) string {
	return cmp.Or(e.Manifest.Name, e.Manifest.ID)
}

// emptyBrowse is what the list says when the search matches nothing.
func (m *model) emptyBrowse() []string {
	query := strings.TrimSpace(m.filters[tabBrowse].Value())
	if query == "" {
		return m.empty("The marketplace index lists no plugins.")
	}
	text := "No marketplace plugin matches “" + query + "”."
	if k := m.keys.name(actClose); k != "" {
		text += " " + k + " clears the search."
	}
	return append(m.empty(text), indent+" "+m.theme.faint.Render("Fewer words match more; owner/repo previews a plugin that is not listed."))
}

// entryLine is the second line of a list item: the description, what else
// matched the search, and the repository's language and last push when the
// description leaves room for them.
func (m *model) entryLine(e market.Entry, terms []string, style lipgloss.Style, width int) string {
	t := m.theme
	name, shown := entryName(e), market.ShownDescription(e, terms)
	visible := name + " " + shown
	var why []string
	// Topics and the source are listed only for a term the rest does not
	// show.
	if matched := market.MatchedTopics(e, terms); len(matched) > 0 && !showsAll(visible, terms) {
		why = append(why, "topics: "+strings.Join(matched, ", "))
		visible += " " + strings.Join(matched, " ")
	}
	if !showsAll(visible, terms) && slices.ContainsFunc(terms, func(term string) bool { return strings.Contains(strings.ToLower(e.Source.String()), term) }) {
		why = append(why, e.Source.String())
	}
	tail := strings.Join(why, " · ")
	room := width - ansi.StringWidth(tail)
	if tail != "" {
		room -= 3
	}
	// What the plugin is for comes first: the meta only shows when the
	// description fits beside it.
	meta := m.entryMeta(e)
	if metaWidth := ansi.StringWidth(meta) + 3; meta != "" && ansi.StringWidth(strings.Join(strings.Fields(shown), " "))+metaWidth <= room {
		room -= metaWidth
	} else {
		meta = ""
	}
	var line string
	switch {
	case shown != "":
		line = m.highlight(market.Snippet(shown, terms, max(room, 20)), terms, style)
	default:
		line = t.faint.Render("No description")
	}
	if tail != "" {
		line += style.Render(" · ") + m.highlight(tail, terms, style)
	}
	if meta != "" {
		line += t.faint.Render(" · " + meta)
	}
	return line
}

// entryMeta is how recently the plugin's repository was pushed to, and
// for a plugin at the repository root the main language GitHub reports for
// it. A plugin in a subdirectory shares its repository with other code, so
// the language is left out and the push is said to be the repository's.
func (m *model) entryMeta(e market.Entry) string {
	r := e.Repo
	var parts []string
	if r.Language != "" && e.Source.Subdir == "" {
		parts = append(parts, r.Language)
	}
	if !r.PushedAt.IsZero() {
		pushed := "pushed " + shortAgo(m.now(), r.PushedAt)
		if e.Source.Subdir != "" {
			pushed = "repository " + pushed
		}
		parts = append(parts, pushed)
	}
	return strings.Join(parts, " · ")
}

// installedAs is how a plugin with e's id is installed here.
type installedAs int

const (
	notInstalled installedAs = iota
	// installedFromListing is an install from the source e lists.
	installedFromListing
	// installedFromElsewhere is an install from another GitHub source.
	installedFromElsewhere
	// linkedLocally is a local plugin linked with the same id.
	linkedLocally
)

// installedRecord finds the installed plugin with e's id and how it relates
// to the listing.
func (m *model) installedRecord(e market.Entry) (herdr.InstalledPluginInfo, installedAs) {
	for _, p := range m.installed {
		if p.PluginID != e.Manifest.ID {
			continue
		}
		src, github := source.FromInstalled(p)
		switch {
		case !github:
			return p, linkedLocally
		case src == e.Source:
			return p, installedFromListing
		}
		return p, installedFromElsewhere
	}
	return herdr.InstalledPluginInfo{}, notInstalled
}

// entryMarks are whether a plugin with e's id is installed, and from where,
// and whether it can run here.
func (m *model) entryMarks(e market.Entry) []string {
	t := m.theme
	var out []string
	p, as := m.installedRecord(e)
	switch as {
	case notInstalled:
	case installedFromListing:
		if ch, ok := m.checks[p.PluginID]; ok && ch.Err == nil && ch.Result.Kind == updates.Available {
			out = append(out, mark(t.warn, glyphUpdate, "update available"))
		} else {
			out = append(out, mark(t.ok, glyphDone, "installed"))
		}
	case installedFromElsewhere:
		out = append(out, mark(t.warn, glyphWarning, "installed from another source"))
	case linkedLocally:
		out = append(out, mark(t.fg2, glyphLocal, "linked locally"))
	}
	if short := m.incompatible(e); short != "" {
		out = append(out, mark(t.err, glyphFailed, short))
	}
	return out
}

// incompatible says in a few words why e cannot run here, or "" when it
// can, as far as the index knows.
func (m *model) incompatible(e market.Entry) string {
	mf := e.Manifest
	switch {
	case len(mf.Platforms) > 0 && !slices.Contains(mf.Platforms, m.platform):
		return "not for " + m.platform
	case len(compat.Problems(nil, mf.MinHerdrVersion, m.herdrVersion, m.platform)) > 0:
		return "needs herdr " + mf.MinHerdrVersion
	}
	return ""
}

// entryPane is the listing beside the list on a wide screen: what the index
// says about the selected plugin, without reading its manifest. The plugin's
// own facts come first, then its repository's.
func (m *model) entryPane(e market.Entry, width int) []string {
	t := m.theme
	out := []string{t.bold.Render(entryName(e)) + "  " + t.faint.Render(e.Manifest.Version+" · "+e.Manifest.ID)}
	if d := e.Description(); d != "" {
		out = append(out, strings.Split(ansi.Wrap(t.fg2.Render(d), width, ""), "\n")...)
	} else {
		out = append(out, t.faint.Render("No description in the manifest or the repository."))
	}
	column := func(rows []field) {
		for _, l := range m.fieldsIn(rows, width+len(indent)) {
			out = append(out, strings.TrimPrefix(l, indent))
		}
	}
	out = append(out, "")
	column(m.entryFields(e))
	out = append(out, "", strings.TrimPrefix(m.heading("Repository", repoNote(e), t.faint), indent))
	column(m.repoFields(e, true))
	if hint := m.press(actOpen, "to see what it runs and install it"); hint != "" {
		out = append(out, "", t.faint.Render(strings.TrimSpace(hint)))
	}
	return out
}

// entryFields are what the index says about the plugin itself: whether it
// is installed, whether it can run here, its version and its topics.
func (m *model) entryFields(e market.Entry) []field {
	t := m.theme
	mf := e.Manifest
	platforms := t.text.Render("any") + t.faint.Render(" (none declared)")
	switch {
	case len(mf.Platforms) == 0:
	case slices.Contains(mf.Platforms, m.platform):
		platforms = t.text.Render(strings.Join(mf.Platforms, ", ")) + " " + t.ok.Render(glyphDone)
	default:
		platforms = t.text.Render(strings.Join(mf.Platforms, ", ")) + " " + t.err.Render(glyphFailed+" not "+m.platform)
	}
	herdrNeed := t.faint.Render("no minimum declared")
	if v := mf.MinHerdrVersion; v != "" {
		herdrNeed = t.text.Render("≥ " + v)
		switch {
		case m.herdrVersion == "":
			herdrNeed += t.faint.Render(" (the running version is unknown)")
		case len(compat.Problems(nil, v, m.herdrVersion, m.platform)) > 0:
			herdrNeed += " " + t.err.Render(glyphFailed+" running "+m.herdrVersion)
		default:
			herdrNeed += " " + t.ok.Render(glyphDone)
		}
	}
	version := t.text.Render(mf.Version) + t.faint.Render(" on the default branch")
	if mf.Version == "" {
		version = t.faint.Render("not declared")
	}
	topics := t.faint.Render("none")
	var names []string
	for _, topic := range e.Repo.Topics {
		if topic != "herdr-plugin" {
			names = append(names, topic)
		}
	}
	if len(names) > 0 {
		topics = t.fg2.Render(strings.Join(names, ", "))
	}
	return []field{
		{"status", m.entryStatus(e)},
		{"platforms", platforms},
		{"herdr", herdrNeed},
		{"version", version},
		{"topics", topics},
	}
}

// repoNote says whose the repository's figures are.
func repoNote(e market.Entry) string {
	if e.Source.Subdir != "" {
		return "GitHub's figures for the whole repository"
	}
	return "GitHub's figures"
}

// repoFields are GitHub's figures for the plugin's repository, one each.
// withSource names the repository and the plugin's folder in it, for a
// screen that does not show the source otherwise.
func (m *model) repoFields(e market.Entry, withSource bool) []field {
	t := m.theme
	r := e.Repo
	var rows []field
	if withSource {
		rows = append(rows, field{"name", t.text.Render(e.Source.Repository())})
		if e.Source.Subdir != "" {
			rows = append(rows, field{"folder", t.text.Render(e.Source.Subdir)})
		}
	}
	stars := t.text.Render(fmt.Sprintf("%s %d", glyphStar, r.Stars))
	if r.StarsDelta7d > 0 {
		stars += t.faint.Render(fmt.Sprintf(" +%d this week", r.StarsDelta7d))
	}
	language := t.faint.Render("not reported")
	if r.Language != "" {
		language = t.text.Render(r.Language)
	}
	pushed := t.faint.Render("not reported")
	if !r.PushedAt.IsZero() {
		pushed = t.text.Render(longAgo(m.now(), r.PushedAt)) + t.faint.Render(" · "+r.PushedAt.Format("2006-01-02"))
	}
	rows = append(rows, field{"stars", stars}, field{"language", language}, field{"last push", pushed})
	if n := len(r.Manifests); n > 1 {
		rows = append(rows, field{"plugins", t.text.Render(fmt.Sprintf("%d in this repository", n))})
	}
	return rows
}

// entryStatus is whether a plugin with e's id is installed here, and when it
// is not this listing, what is.
func (m *model) entryStatus(e market.Entry) string {
	t := m.theme
	p, as := m.installedRecord(e)
	switch as {
	case notInstalled:
		return t.faint.Render("not installed")
	case installedFromElsewhere:
		return mark(t.warn, glyphWarning, "installed from "+manager.SourceLabel(p)) + t.faint.Render(", not this listing; installing this one replaces it")
	case linkedLocally:
		return mark(t.fg2, glyphLocal, "linked locally from "+p.PluginRoot) + t.faint.Render("; unlink it to install this listing")
	case installedFromListing:
	}
	s := mark(t.ok, glyphDone, "installed") + " " + t.faint.Render(p.Version)
	if ch, ok := m.checks[p.PluginID]; ok && ch.Err == nil && ch.Result.Kind == updates.Available {
		s += "  " + mark(t.warn, glyphUpdate, "update available")
	}
	return s
}

// shortAgo is how long ago t was, in a few cells: "today", "3d ago",
// "5mo ago" or "2y ago".
func shortAgo(now, t time.Time) string {
	days := int(now.Sub(t).Hours() / 24)
	switch {
	case days < 1:
		return "today"
	case days < 60:
		return fmt.Sprintf("%dd ago", days)
	case days < 730:
		return fmt.Sprintf("%dmo ago", days/30)
	}
	return fmt.Sprintf("%dy ago", days/365)
}

// longAgo is shortAgo in words.
func longAgo(now, t time.Time) string {
	days := int(now.Sub(t).Hours() / 24)
	switch {
	case days < 1:
		return "today"
	case days == 1:
		return "yesterday"
	case days < 60:
		return fmt.Sprintf("%d days ago", days)
	case days < 730:
		return fmt.Sprintf("%d months ago", days/30)
	}
	return fmt.Sprintf("%d years ago", days/365)
}
