package ui

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"

	"github.com/vika2603/herdr-plugin-manager/internal/config"
)

// palette is the colour of every role on one terminal background. docs/design.md
// says what each role is for; the config file's [theme.dark] and
// [theme.light] tables replace roles by these names.
type palette struct {
	Accent   string
	FG       string
	FG2      string
	FG3      string
	Rule     string
	Surface  string
	OK       string
	Warn     string
	Danger   string
	OnAccent string
}

var basePalettes = [2]palette{
	light: {
		FG: "#1C1C22", FG2: "#5C5C68", FG3: "#8E8E9A", Rule: "#DCDCE3", Surface: "#ECECF1",
		OK: "#1E8E63", Warn: "#A86A00", Danger: "#C8323F", OnAccent: "#FFFFFF",
	},
	dark: {
		FG: "#E6E6EA", FG2: "#A3A3AE", FG3: "#6C6C78", Rule: "#33333D", Surface: "#2B2B34",
		OK: "#3FB68B", Warn: "#E8B04B", Danger: "#F0616D", OnAccent: "#17171C",
	},
}

// Indexes of basePalettes and accentPresets.
const (
	light = 0
	dark  = 1
)

// accentPresets are the accents the config file names, for a light and a
// dark background.
var accentPresets = map[string][2]string{
	"indigo":  {"#4B55D6", "#8B93FF"},
	"teal":    {"#0F8C7D", "#3CC8B4"},
	"magenta": {"#D6336C", "#F25D94"},
}

const defaultAccent = "indigo"

var hexColor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// palettes resolves the [theme] table: the accent, then any role replaced
// for a background. With an error the default palettes are returned.
func palettes(t config.Theme) ([2]palette, error) {
	out := basePalettes
	accent := t.Accent
	if accent == "" {
		accent = defaultAccent
	}
	switch preset, ok := accentPresets[accent]; {
	case ok:
		out[light].Accent, out[dark].Accent = preset[light], preset[dark]
	case hexColor.MatchString(accent):
		out[light].Accent, out[dark].Accent = accent, accent
	default:
		return defaultPalettes(), fmt.Errorf("config: theme accent %q is not indigo, teal, magenta or a #RRGGBB colour", accent)
	}
	for i, roles := range [2]map[string]string{light: t.Light, dark: t.Dark} {
		for role, value := range roles {
			field := out[i].role(role)
			if field == nil {
				return defaultPalettes(), fmt.Errorf("config: unknown theme colour %q", role)
			}
			if !hexColor.MatchString(value) {
				return defaultPalettes(), fmt.Errorf("config: theme colour %s = %q is not a #RRGGBB colour", role, value)
			}
			*field = value
		}
	}
	return out, nil
}

func defaultPalettes() [2]palette {
	p, _ := palettes(config.Theme{})
	return p
}

// themeModes are the values of [theme] mode.
var themeModes = []string{"", "auto", "dark", "light"}

// role is the field of role name, or nil.
func (p *palette) role(name string) *string {
	switch name {
	case "accent":
		return &p.Accent
	case "fg":
		return &p.FG
	case "fg2":
		return &p.FG2
	case "fg3":
		return &p.FG3
	case "rule":
		return &p.Rule
	case "surface":
		return &p.Surface
	case "ok":
		return &p.OK
	case "warn":
		return &p.Warn
	case "danger":
		return &p.Danger
	case "on_accent":
		return &p.OnAccent
	}
	return nil
}

// mix blends a toward b by the fraction of b given, in sRGB.
func mix(a, b string, fraction float64) string {
	parse := func(s string) [3]float64 {
		v, _ := strconv.ParseUint(s[1:], 16, 32)
		return [3]float64{float64(v >> 16 & 0xff), float64(v >> 8 & 0xff), float64(v & 0xff)}
	}
	x, y := parse(a), parse(b)
	var c [3]int
	for i := range c {
		c[i] = int(x[i] + (y[i]-x[i])*fraction + 0.5)
	}
	return fmt.Sprintf("#%02X%02X%02X", c[0], c[1], c[2])
}

// theme holds every style the views draw with, derived from one palette.
type theme struct {
	dark bool

	// Focus: what is selected or current.
	accent     lipgloss.Style
	accentBold lipgloss.Style
	soft       lipgloss.Style
	selBar     lipgloss.Style
	match      lipgloss.Style

	// Text in three levels, and structure.
	text  lipgloss.Style
	bold  lipgloss.Style
	fg2   lipgloss.Style
	faint lipgloss.Style
	label lipgloss.Style
	rule  lipgloss.Style

	// State.
	ok     lipgloss.Style
	warn   lipgloss.Style
	err    lipgloss.Style
	okBold lipgloss.Style

	tabActive   lipgloss.Style
	tabInactive lipgloss.Style
	tabCount    lipgloss.Style
	keyChip     lipgloss.Style
	keyChipMain lipgloss.Style
	keyDesc     lipgloss.Style

	dialog lipgloss.Style
	help   help.Styles
	input  textinput.Styles
}

func newTheme(isDark bool, p palette) theme {
	c := lipgloss.Color
	fg := func(hex string) lipgloss.Style { return lipgloss.NewStyle().Foreground(c(hex)) }
	soft := mix(p.Accent, p.FG2, 0.45)

	t := theme{
		dark: isDark,

		accent:     fg(p.Accent),
		accentBold: fg(p.Accent).Bold(true),
		soft:       fg(soft),
		selBar:     fg(p.Accent),
		match:      fg(p.Accent).Bold(true),

		text:  fg(p.FG),
		bold:  fg(p.FG).Bold(true),
		fg2:   fg(p.FG2),
		faint: fg(p.FG3),
		label: fg(p.FG3),
		rule:  fg(p.Rule),

		ok:     fg(p.OK),
		warn:   fg(p.Warn),
		err:    fg(p.Danger),
		okBold: fg(p.OK).Bold(true),

		tabActive:   fg(p.Accent).Bold(true),
		tabInactive: fg(p.FG2),
		tabCount:    fg(p.FG3),
		keyChip:     lipgloss.NewStyle().Foreground(c(p.FG)).Background(c(p.Surface)).Padding(0, 1),
		keyChipMain: lipgloss.NewStyle().Foreground(c(p.OnAccent)).Background(c(p.Accent)).Bold(true).Padding(0, 1),
		keyDesc:     fg(p.FG2),

		dialog: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(c(p.Accent)).Padding(1, 3),
		help:   help.DefaultStyles(isDark),
		input:  textinput.DefaultStyles(isDark),
	}
	t.help.FullKey = t.keyChip
	t.help.FullDesc = t.keyDesc
	t.input.Focused.Prompt = t.accent
	t.input.Blurred.Prompt = t.faint
	t.input.Focused.Text = t.text
	t.input.Focused.Placeholder = t.faint
	t.input.Blurred.Placeholder = t.faint
	return t
}

// themeFor builds the theme of a background from the resolved palettes.
func themeFor(isDark bool, p [2]palette) theme {
	if isDark {
		return newTheme(true, p[dark])
	}
	return newTheme(false, p[light])
}

func validMode(mode string) bool { return slices.Contains(themeModes, mode) }
