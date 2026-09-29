package ui

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/vika2603/herdr-plugin-manager/internal/app"
	"github.com/vika2603/herdr-plugin-manager/internal/config"
	"github.com/vika2603/herdr-plugin-manager/internal/manager"
	"github.com/vika2603/herdr-plugin-manager/internal/safe"
)

// ConfigFinding is how the manager's config file in dir stands: loadErr is
// why it could not be read, and its keys and theme are checked as the
// manager checks them on start, replacing what is wrong by the defaults.
func ConfigFinding(dir string, cfg config.Config, loadErr error) manager.Finding {
	f := manager.Finding{Area: "hpm config", Health: manager.Healthy}
	if dir == "" {
		f.Health, f.Summary = manager.Warning, "herdr could not report the plugin config directory, so the defaults apply"
		return f
	}
	path := filepath.Join(dir, config.File)
	if loadErr != nil {
		f.Details = append(f.Details, loadErr.Error())
	}
	if _, err := newKeymap(cfg.Keys); err != nil {
		f.Details = append(f.Details, err.Error()+"; the default keys apply")
	}
	if _, err := palettes(cfg.Theme); err != nil {
		f.Details = append(f.Details, err.Error()+"; the default colours apply")
	}
	if !validMode(cfg.Theme.Mode) {
		f.Details = append(f.Details, fmt.Sprintf("theme mode %q is not auto, dark or light", cfg.Theme.Mode))
	}
	if len(f.Details) > 0 {
		f.Health, f.Summary = manager.Warning, path+" has problems"
		return f
	}
	f.Summary = path + " is valid"
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		f.Summary = "no config file at " + path + "; the defaults apply"
	}
	return f
}

// doctorView is the diagnostics screen: what the doctor found, scrolled by
// line.
type doctorView struct {
	findings []manager.Finding
	loaded   bool
	offset   int
}

type doctorMsg struct {
	v        *doctorView
	findings []manager.Finding
}

// openDoctor checks everything the manager depends on, and the manager's
// own config as it was read on start.
func (m *model) openDoctor() tea.Cmd {
	v := &doctorView{}
	m.doctor, m.screen = v, screenDoctor
	opts := m.opts
	return m.withSpinner(func() tea.Msg {
		findings := m.b.Doctor(m.ctx, app.MinHerdrVersion)
		findings = append(findings, ConfigFinding(opts.ConfigDir, opts.Config, opts.ConfigErr))
		return doctorMsg{v: v, findings: findings}
	})
}

func onDoctor(msg doctorMsg) { msg.v.findings, msg.v.loaded = msg.findings, true }

func (m *model) keyDoctor(a action) (tea.Model, tea.Cmd) {
	v := m.doctor
	switch a {
	case actUp:
		v.offset = max(v.offset-1, 0)
	case actDown:
		v.offset++
	case actPageUp:
		v.offset = max(v.offset-m.bodyHeight(), 0)
	case actPageDown:
		v.offset += m.bodyHeight()
	case actTop:
		v.offset = 0
	case actReload:
		return m, m.openDoctor()
	case actHelp:
		m.showHelp = !m.showHelp
	case actClose, actQuit:
		m.screen, m.doctor = screenList, nil
	default:
	}
	return m, nil
}

func (m *model) viewDoctor() string {
	t := m.theme
	v := m.doctor
	var lines []string
	if !v.loaded {
		lines = []string{indent + t.faint.Render("Checking…")}
	}
	for _, f := range v.findings {
		var state string
		switch f.Health {
		case manager.Healthy:
			state = t.ok.Render(glyphDone)
		case manager.Warning:
			state = t.warn.Render(glyphWarning)
		default:
			state = t.err.Render(glyphFailed)
		}
		head := t.bold.Render(f.Area) + "  " + t.text.Render(safe.Line(f.Summary))
		for i, l := range strings.Split(ansi.Wrap(head, max(m.w()-1-len(subIndent), 10), ""), "\n") {
			if i == 0 {
				lines = append(lines, indent+state+" "+l)
			} else {
				lines = append(lines, subIndent+l)
			}
		}
		for _, d := range f.Details {
			lines = append(lines, wrapIndented(subIndent+t.fg2.Render(safe.Line(d)), m.w()-1)...)
		}
	}
	v.offset = min(v.offset, max(len(lines)-m.bodyHeight(), 0))
	return m.frame(m.crumbs(tabNames[m.tab], "Diagnostics"), nil, lines[v.offset:])
}

// doctorStatus is the status line of the diagnostics: how many areas fail
// and warn.
func (m *model) doctorStatus() string {
	t := m.theme
	v := m.doctor
	if !v.loaded {
		return " " + m.spinner.View() + " " + t.fg2.Render("Checking herdr, GitHub and the plugins…")
	}
	var failing, warning int
	for _, f := range v.findings {
		switch f.Health {
		case manager.Failing:
			failing++
		case manager.Warning:
			warning++
		case manager.Healthy:
		}
	}
	summary := fmt.Sprintf("%d failing, %d with warnings, of %d checked", failing, warning, len(v.findings))
	style := t.text
	if failing > 0 {
		style = t.err
	}
	return " " + style.Render(summary) + t.faint.Render(m.keyHint(actReload, "checks again"))
}
