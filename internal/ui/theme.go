package ui

import (
	"image/color"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
)

// theme holds every style the views draw with. Colours come in light and dark
// variants; the terminal's background, when it reports one, picks between
// them.
type theme struct {
	dark bool

	tabActive   lipgloss.Style
	tabInactive lipgloss.Style
	tabCount    lipgloss.Style
	tabLine     lipgloss.Style
	rule        lipgloss.Style
	intro       lipgloss.Style
	crumb       lipgloss.Style
	crumbTitle  lipgloss.Style
	version     lipgloss.Style
	keyChip     lipgloss.Style
	keyChipMain lipgloss.Style
	keyDesc     lipgloss.Style
	match       lipgloss.Style

	itemTitle lipgloss.Style
	itemDesc  lipgloss.Style
	selTitle  lipgloss.Style
	selDesc   lipgloss.Style
	selBar    lipgloss.Style

	faint   lipgloss.Style
	accent  lipgloss.Style
	ok      lipgloss.Style
	warn    lipgloss.Style
	err     lipgloss.Style
	heading lipgloss.Style
	code    lipgloss.Style
	star    lipgloss.Style

	badgeEnabled   lipgloss.Style
	badgeDisabled  lipgloss.Style
	badgeUpdate    lipgloss.Style
	badgeInstalled lipgloss.Style
	badgeLocal     lipgloss.Style
	badgeError     lipgloss.Style
	badgeWarn      lipgloss.Style

	dialog lipgloss.Style
	help   help.Styles
	input  textinput.Styles
}

func newTheme(dark bool) theme {
	ld := lipgloss.LightDark(dark)
	var (
		pink  = lipgloss.Color("#F25D94")
		cream = lipgloss.Color("#FFFDF5")
		green = lipgloss.Color("#04B575")
		red   = lipgloss.Color("#FF4672")
		ink   = lipgloss.Color("#1A1A1A")

		text     = ld(lipgloss.Color("#1A1A1A"), lipgloss.Color("#DDDDDD"))
		subtle   = ld(lipgloss.Color("#A49FA5"), lipgloss.Color("#777777"))
		faint    = ld(lipgloss.Color("#9B9B9B"), lipgloss.Color("#5C5C5C"))
		selTitle = ld(lipgloss.Color("#EE6FF8"), lipgloss.Color("#EE6FF8"))
		selDesc  = ld(lipgloss.Color("#F793FF"), lipgloss.Color("#AD58B4"))
		heading  = ld(lipgloss.Color("#7D56F4"), lipgloss.Color("#A48CFF"))
		yellow   = ld(lipgloss.Color("#F2C94C"), lipgloss.Color("#ECFD65"))
		amber    = ld(lipgloss.Color("#B7791F"), lipgloss.Color("#F5C451"))
		code     = ld(lipgloss.Color("#5A5A5A"), lipgloss.Color("#C1C6B2"))
		muted    = ld(lipgloss.Color("#E4E4E4"), lipgloss.Color("#3A3A3A"))
		mutedFg  = ld(lipgloss.Color("#6C6C6C"), lipgloss.Color("#A49FA5"))
		localBg  = ld(lipgloss.Color("#E9E3FF"), lipgloss.Color("#3B2F63"))
		localFg  = ld(lipgloss.Color("#5B3CC4"), lipgloss.Color("#C9BCFF"))
	)
	badge := func(fg, bg color.Color) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(fg).Background(bg).Padding(0, 1)
	}

	t := theme{
		dark:        dark,
		tabActive:   lipgloss.NewStyle().Foreground(pink).Bold(true),
		tabInactive: lipgloss.NewStyle().Foreground(subtle),
		tabCount:    lipgloss.NewStyle().Foreground(faint),
		tabLine:     lipgloss.NewStyle().Foreground(pink),
		rule:        lipgloss.NewStyle().Foreground(muted),
		intro:       lipgloss.NewStyle().Foreground(subtle),
		crumb:       lipgloss.NewStyle().Foreground(subtle),
		crumbTitle:  lipgloss.NewStyle().Foreground(text).Bold(true),
		version:     lipgloss.NewStyle().Foreground(faint),
		keyChip:     lipgloss.NewStyle().Foreground(text).Background(muted).Padding(0, 1),
		keyChipMain: lipgloss.NewStyle().Foreground(cream).Background(pink).Bold(true).Padding(0, 1),
		keyDesc:     lipgloss.NewStyle().Foreground(subtle),
		match:       lipgloss.NewStyle().Foreground(amber).Bold(true).Underline(true),

		itemTitle: lipgloss.NewStyle().Foreground(text),
		itemDesc:  lipgloss.NewStyle().Foreground(subtle),
		selTitle:  lipgloss.NewStyle().Foreground(selTitle).Bold(true),
		selDesc:   lipgloss.NewStyle().Foreground(selDesc),
		selBar:    lipgloss.NewStyle().Foreground(selDesc),

		faint:   lipgloss.NewStyle().Foreground(faint),
		accent:  lipgloss.NewStyle().Foreground(pink),
		ok:      lipgloss.NewStyle().Foreground(green),
		warn:    lipgloss.NewStyle().Foreground(amber),
		err:     lipgloss.NewStyle().Foreground(red),
		heading: lipgloss.NewStyle().Foreground(heading).Bold(true),
		code:    lipgloss.NewStyle().Foreground(code),
		star:    lipgloss.NewStyle().Foreground(amber),

		badgeEnabled:   badge(cream, green),
		badgeDisabled:  badge(mutedFg, muted),
		badgeUpdate:    badge(ink, yellow),
		badgeInstalled: badge(cream, green),
		badgeLocal:     badge(localFg, localBg),
		badgeError:     badge(cream, red),
		badgeWarn:      badge(ink, amber),

		dialog: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(pink).Padding(1, 3),
		help:   help.DefaultStyles(dark),
		input:  textinput.DefaultStyles(dark),
	}
	t.help.FullKey = t.keyChip
	t.help.FullDesc = t.keyDesc
	t.input.Focused.Prompt = lipgloss.NewStyle().Foreground(pink)
	t.input.Blurred.Prompt = lipgloss.NewStyle().Foreground(faint)
	t.input.Focused.Placeholder = lipgloss.NewStyle().Foreground(faint)
	t.input.Blurred.Placeholder = lipgloss.NewStyle().Foreground(faint)
	return t
}
