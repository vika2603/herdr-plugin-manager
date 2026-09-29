package ui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/vika2603/herdr-plugin-manager/internal/compat"
	"github.com/vika2603/herdr-plugin-manager/internal/market"
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
	t := m.theme
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
	installed := m.installedIDs()
	terms := market.Terms(m.filters[tabBrowse].Value())
	var out []string
	start, end := m.offset[tabBrowse], min(m.offset[tabBrowse]+m.pageSize(), len(entries))
	for i := start; i < end; i++ {
		e := entries[i]
		selected := i == m.cursor[tabBrowse]
		nameStyle, descStyle := m.itemStyles(selected)
		parts := []string{
			m.highlight(entryName(e), terms, nameStyle),
			t.faint.Render(e.Manifest.Version), t.faint.Render(fmt.Sprintf("%s %d", glyphStar, e.Repo.Stars)),
		}
		title := strings.Join(append(parts, m.entryMarks(e, installed)...), "  ")
		out = append(out, m.item(title, m.entryLine(e, terms, descStyle, width-4), selected)...)
	}
	return out
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
// matched the search, and the repository's language and last push when
// there is room.
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
	meta := m.entryMeta(e)
	room := width - ansi.StringWidth(tail)
	if tail != "" {
		room -= 3
	}
	if meta != "" && room-ansi.StringWidth(meta)-3 >= 24 {
		room -= ansi.StringWidth(meta) + 3
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

// entryMeta is the repository's language and last push, which for a
// plugin in a subdirectory are the whole repository's, so marked "repo".
func (m *model) entryMeta(e market.Entry) string {
	var parts []string
	if l := e.Repo.Language; l != "" {
		parts = append(parts, l)
	}
	if !e.Repo.PushedAt.IsZero() {
		parts = append(parts, "pushed "+shortAgo(m.now(), e.Repo.PushedAt))
	}
	if len(parts) == 0 {
		return ""
	}
	s := strings.Join(parts, " · ")
	if e.Source.Subdir != "" {
		s = "repo " + s
	}
	return s
}

// entryMarks are an entry's installed state and whether it can run here.
func (m *model) entryMarks(e market.Entry, installed map[string]bool) []string {
	t := m.theme
	var out []string
	if installed[e.Manifest.ID] {
		if ch, ok := m.checks[e.Manifest.ID]; ok && ch.Err == nil && ch.Result.Kind == updates.Available {
			out = append(out, mark(t.warn, glyphUpdate, "update available"))
		} else {
			out = append(out, mark(t.ok, glyphDone, "installed"))
		}
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
	platform := compat.Platform()
	switch {
	case len(mf.Platforms) > 0 && !slices.Contains(mf.Platforms, platform):
		return "not for " + platform
	case len(compat.Problems(nil, mf.MinHerdrVersion, m.herdrVersion, platform)) > 0:
		return "needs herdr " + mf.MinHerdrVersion
	}
	return ""
}

// entryPane is the listing beside the list on a wide screen: what the index
// says about the selected plugin, without reading its manifest.
func (m *model) entryPane(e market.Entry, width int) []string {
	t := m.theme
	out := []string{t.bold.Render(entryName(e)) + "  " + t.faint.Render(e.Manifest.Version+" · "+e.Manifest.ID)}
	if d := e.Description(); d != "" {
		out = append(out, strings.Split(ansi.Wrap(t.fg2.Render(d), width, ""), "\n")...)
	} else {
		out = append(out, t.faint.Render("No description in the manifest or the repository."))
	}
	out = append(out, "")
	// The link is left to the plugin's screen; the source says where it is.
	rows := slices.DeleteFunc(m.entryFields(e), func(f field) bool { return f.label == "link" })
	for _, l := range m.fieldsIn(rows, width+len(indent)) {
		out = append(out, strings.TrimPrefix(l, indent))
	}
	mf := e.Manifest
	for _, p := range compat.Problems(mf.Platforms, mf.MinHerdrVersion, m.herdrVersion, compat.Platform()) {
		out = append(out, strings.Split(ansi.Wrap(t.err.Render(glyphFailed+" "+p), width, ""), "\n")...)
	}
	if hint := m.press(actOpen, "to see what it runs and install it"); hint != "" {
		out = append(out, "", t.faint.Render(strings.TrimSpace(hint)))
	}
	return out
}

// entryFields are the rows of what the index says about e. The language,
// stars and dates belong to the repository, which for a plugin in a
// subdirectory holds more than the plugin, and are labelled so.
func (m *model) entryFields(e market.Entry) []field {
	t := m.theme
	mf := e.Manifest
	problems := compat.Problems(mf.Platforms, mf.MinHerdrVersion, m.herdrVersion, compat.Platform())
	rows := []field{{"status", m.entryStatus(e)}, {"runs on", m.runsOn(mf.Platforms, mf.MinHerdrVersion, len(problems) == 0)}}
	version := t.text.Render(mf.Version) + t.faint.Render(" in the manifest on the default branch")
	if mf.Version == "" {
		version = t.faint.Render("not declared")
	}
	rows = append(rows,
		field{"version", version},
		field{"source", t.text.Render(e.Source.String())},
		field{"link", t.fg2.Render(e.Source.WebURL())},
	)
	var topics []string
	for _, topic := range e.Repo.Topics {
		if topic != "herdr-plugin" {
			topics = append(topics, topic)
		}
	}
	if len(topics) > 0 {
		rows = append(rows, field{"topics", t.fg2.Render(strings.Join(topics, ", "))})
	} else {
		rows = append(rows, field{"topics", t.faint.Render("none")})
	}
	return append(rows, field{"repository", m.repoSummary(e)})
}

// repoSummary is the repository's stars, language and dates.
func (m *model) repoSummary(e market.Entry) string {
	t := m.theme
	r := e.Repo
	stars := fmt.Sprintf("%s %d", glyphStar, r.Stars)
	if r.StarsDelta7d > 0 {
		stars += fmt.Sprintf(" (+%d this week)", r.StarsDelta7d)
	}
	parts := []string{t.text.Render(stars)}
	if r.Language != "" {
		parts = append(parts, t.text.Render(r.Language))
	} else {
		parts = append(parts, t.faint.Render("language not reported"))
	}
	if !r.PushedAt.IsZero() {
		parts = append(parts, t.text.Render("last push "+r.PushedAt.Format("2006-01-02")+" ("+longAgo(m.now(), r.PushedAt)+")"))
	} else {
		parts = append(parts, t.faint.Render("last push not reported"))
	}
	if !r.CreatedAt.IsZero() {
		parts = append(parts, t.fg2.Render("created "+r.CreatedAt.Format("2006-01-02")))
	}
	if n := len(r.Manifests); n > 1 {
		parts = append(parts, t.fg2.Render(fmt.Sprintf("%d plugins in it", n)))
	}
	s := strings.Join(parts, t.faint.Render(" · "))
	if e.Source.Subdir != "" {
		s += t.faint.Render(" · the plugin is in " + e.Source.Subdir)
	}
	return s
}

// entryStatus is whether e is installed here.
func (m *model) entryStatus(e market.Entry) string {
	t := m.theme
	for _, p := range m.installed {
		if p.PluginID != e.Manifest.ID {
			continue
		}
		s := mark(t.ok, glyphDone, "installed") + " " + t.faint.Render(p.Version)
		if ch, ok := m.checks[p.PluginID]; ok && ch.Err == nil && ch.Result.Kind == updates.Available {
			s += "  " + mark(t.warn, glyphUpdate, "update available")
		}
		return s
	}
	return t.faint.Render("not installed")
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
