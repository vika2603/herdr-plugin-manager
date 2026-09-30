package manager

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-plugin-manager/internal/compat"
)

// Health is how a diagnosed area stands.
type Health string

// Health values, from good to bad.
const (
	Healthy Health = "ok"
	Warning Health = "warn"
	Failing Health = "fail"
)

// Finding is what the doctor found about one area: herdr, its server, the
// key bindings, GitHub, one plugin.
type Finding struct {
	Area    string   `json:"area"`
	Health  Health   `json:"status"`
	Summary string   `json:"summary"`
	Details []string `json:"details,omitempty"`
}

// rateLimitLow is when few enough GitHub API requests are left to warn.
const rateLimitLow = 10

// Doctor checks what the manager depends on and the installed plugins.
// needHerdr is the herdr version the manager needs. It changes nothing.
func (m *Manager) Doctor(ctx context.Context, needHerdr string) []Finding {
	cliVersion, herdrFinding := m.checkHerdr(ctx, needHerdr)
	out := []Finding{herdrFinding, m.checkServer(ctx, cliVersion), m.checkHerdrConfig(ctx)}
	plugins, listErr := m.Installed(ctx)
	if listErr == nil {
		out = append(out, checkBindings(HerdrConfigPath(), plugins))
	}
	out = append(out, checkGit(ctx), m.checkGitHub(ctx), m.checkIndex(), m.checkHistory())
	if listErr != nil {
		return append(out, Finding{Area: "plugins", Health: Failing, Summary: "the installed plugins could not be listed: " + listErr.Error()})
	}
	return append(out, m.checkPlugins(plugins, m.HerdrVersion(ctx))...)
}

func (m *Manager) checkHerdr(ctx context.Context, need string) (string, Finding) {
	f := Finding{Area: "herdr"}
	v, err := m.CLI.Version(ctx)
	if err != nil {
		f.Health, f.Summary = Failing, "the herdr command could not be run: "+err.Error()
		f.Details = []string{"installs, updates and uninstalls run it; install herdr, or run hpm from herdr"}
		return "", f
	}
	path := m.CLI.Bin
	if path == "" {
		path, _ = exec.LookPath("herdr")
	}
	f.Health, f.Summary = Healthy, "herdr "+v+" at "+path
	if p := compat.Problems(nil, need, v, ""); len(p) > 0 {
		f.Health, f.Summary = Failing, "herdr "+v+" at "+path+" is older than the "+need+" this manager needs"
	}
	return v, f
}

func (m *Manager) checkServer(ctx context.Context, cliVersion string) Finding {
	f := Finding{Area: "server"}
	down := []string{"without it, plugins cannot be enabled or disabled, command logs cannot be read, and a disabled plugin cannot be updated, since herdr enables every plugin it installs"}
	api, err := m.api()
	if err != nil {
		f.Health, f.Summary, f.Details = Warning, "no herdr server to talk to: "+err.Error(), down
		return f
	}
	pong, err := api.Ping(ctx)
	if err != nil {
		f.Health, f.Summary, f.Details = Warning, "no herdr server answers: "+serverErr(err).Error(), down
		return f
	}
	f.Health, f.Summary = Healthy, "herdr server "+pong.Version+" is running"
	if cliVersion != "" && pong.Version != cliVersion {
		f.Health = Warning
		f.Summary = fmt.Sprintf("the herdr server runs %s but the herdr command is %s", pong.Version, cliVersion)
		f.Details = []string{"restart the server so both are the same version"}
	}
	return f
}

func (m *Manager) checkHerdrConfig(ctx context.Context) Finding {
	path := HerdrConfigPath()
	f := Finding{Area: "herdr config"}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		f.Health, f.Summary = Healthy, "no config file at "+path+"; herdr uses its defaults"
		return f
	}
	issues, err := m.CLI.ConfigCheck(ctx)
	switch {
	case err != nil:
		f.Health, f.Summary = Warning, "herdr config check failed: "+err.Error()
	case len(issues) > 0:
		f.Health, f.Summary, f.Details = Warning, path+" has problems, as herdr config check reports", issues
	default:
		f.Health, f.Summary = Healthy, path+" is valid"
	}
	return f
}

// checkBindings finds the keys in herdr's config at path that run a plugin
// action no installed plugin declares, or a disabled one.
func checkBindings(path string, plugins []herdr.InstalledPluginInfo) Finding {
	f := Finding{Area: "key bindings"}
	bindings, err := keyBindings(path)
	if err != nil {
		f.Health, f.Summary = Warning, "the key bindings could not be read: "+err.Error()
		return f
	}
	n := 0
	for _, b := range bindings {
		if b.Type != "plugin_action" {
			continue
		}
		n++
		var owners []herdr.InstalledPluginInfo
		for _, p := range plugins {
			for _, a := range p.Actions.ValueOrZero() {
				if b.Command == p.PluginID+"."+a.ID || b.Command == a.ID {
					owners = append(owners, p)
				}
			}
		}
		switch {
		case len(owners) == 0:
			f.Details = append(f.Details, fmt.Sprintf("%s runs %s, which no installed plugin declares", b.Key, b.Command))
		case len(owners) > 1:
			f.Details = append(f.Details, fmt.Sprintf("%s runs %s, which more than one plugin declares; name it as <plugin id>.<action id>", b.Key, b.Command))
		case !owners[0].Enabled:
			f.Details = append(f.Details, fmt.Sprintf("%s runs %s, but %s is disabled", b.Key, b.Command, owners[0].PluginID))
		}
	}
	switch {
	case n == 0:
		f.Health, f.Summary = Healthy, "no key runs a plugin action"
	case len(f.Details) > 0:
		f.Health, f.Summary = Warning, fmt.Sprintf("%d of %d keys bound to plugin actions have problems", len(f.Details), n)
	default:
		f.Health, f.Summary = Healthy, fmt.Sprintf("%d keys run plugin actions, each an enabled plugin's", n)
		if n == 1 {
			f.Summary = "1 key runs a plugin action, an enabled plugin's"
		}
	}
	return f
}

func checkGit(ctx context.Context) Finding {
	f := Finding{Area: "git"}
	out, err := exec.CommandContext(ctx, "git", "--version").Output()
	if err != nil {
		f.Health, f.Summary = Failing, "git could not be run: "+err.Error()
		f.Details = []string{"update checks and version lists read repositories with git"}
		return f
	}
	f.Health, f.Summary = Healthy, strings.TrimSpace(string(out))
	return f
}

func (m *Manager) checkGitHub(ctx context.Context) Finding {
	f := Finding{Area: "GitHub"}
	if m.Market == nil {
		f.Health, f.Summary = Warning, "not checked"
		return f
	}
	rl, err := m.Market.RateLimit(ctx)
	if err != nil {
		f.Health, f.Summary = Failing, "GitHub's API could not be reached: "+err.Error()
		f.Details = []string{"release notes, commit lists and READMEs are read from it"}
		return f
	}
	f.Health = Healthy
	f.Summary = fmt.Sprintf("%d of %d API requests left, until %s", rl.Remaining, rl.Limit, rl.Reset.Local().Format(time.TimeOnly))
	if rl.Remaining < rateLimitLow {
		f.Health = Warning
	}
	if rl.Token {
		f.Details = []string{"requests carry the token in GH_TOKEN or GITHUB_TOKEN"}
	} else {
		f.Details = []string{"no token: set GH_TOKEN or GITHUB_TOKEN to raise the limit"}
	}
	return f
}

func (m *Manager) checkIndex() Finding {
	f := Finding{Area: "marketplace"}
	if m.Market == nil {
		f.Health, f.Summary = Warning, "not checked"
		return f
	}
	n, at, err := m.Market.CacheStatus()
	switch {
	case errors.Is(err, fs.ErrNotExist):
		f.Health, f.Summary = Healthy, "the plugin index has not been downloaded yet; the marketplace downloads it"
	case err != nil:
		f.Health, f.Summary = Warning, "the cached plugin index could not be read: "+err.Error()
	default:
		f.Health, f.Summary = Healthy, fmt.Sprintf("the plugin index lists %d plugins, downloaded %s", n, at.Local().Format(time.DateTime))
	}
	return f
}

func (m *Manager) checkHistory() Finding {
	f := Finding{Area: "history"}
	h := m.History
	if h == nil || h.Dir == "" {
		f.Health, f.Summary = Warning, "no state directory, so changes are not recorded and cannot be rolled back"
		return f
	}
	info, err := os.Stat(h.Dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		f.Health, f.Summary = Healthy, "no change recorded yet; changes are kept in "+h.Dir
		return f
	case err != nil:
		f.Health, f.Summary = Failing, "the state directory cannot be read: "+err.Error()
		return f
	case !info.IsDir() || !directoryWritable(h.Dir):
		f.Health, f.Summary = Failing, h.Dir+" cannot be written to, so changes are not recorded"
		return f
	}
	entries, err := h.List()
	if err != nil {
		f.Health, f.Summary = Failing, "the history cannot be read: "+err.Error()
		return f
	}
	f.Health, f.Summary = Healthy, fmt.Sprintf("%d changes recorded in %s", len(entries), h.Dir)
	if _, err := h.follows(); err != nil {
		f.Health = Warning
		f.Details = []string{"the refs kept for plugins installed at a commit cannot be read, so they are listed as pinned: " + err.Error()}
	}
	return f
}

// directoryWritable tests the actual ability to create history files. File
// mode bits alone cannot establish this on Windows or with ACLs.
func directoryWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".hpm-write-*")
	if err != nil {
		return false
	}
	name := f.Name()
	closeErr := f.Close()
	removeErr := os.Remove(name)
	return closeErr == nil && removeErr == nil
}

// checkPlugins reports each installed plugin that cannot run here, whose
// directory is gone or that herdr warns about.
func (m *Manager) checkPlugins(plugins []herdr.InstalledPluginInfo, herdrVersion string) []Finding {
	var out []Finding
	for _, p := range plugins {
		var platforms []string
		for _, pl := range p.Platforms.ValueOrZero() {
			platforms = append(platforms, string(pl))
		}
		f := Finding{Area: "plugin " + p.PluginID, Health: Healthy}
		if problems := compat.Problems(platforms, p.MinHerdrVersion.ValueOrZero(), herdrVersion, m.Platform); len(problems) > 0 {
			f.Health, f.Details = Failing, problems
		}
		if _, err := os.Stat(p.PluginRoot); err != nil {
			f.Health = Failing
			f.Details = append(f.Details, "its directory "+p.PluginRoot+" is missing")
		}
		if w := p.Warnings.ValueOrZero(); len(w) > 0 {
			if f.Health == Healthy {
				f.Health = Warning
			}
			for _, warning := range w {
				f.Details = append(f.Details, "herdr warns: "+warning)
			}
		}
		if f.Health == Healthy {
			continue
		}
		f.Summary = "cannot run here"
		if f.Health == Warning {
			f.Summary = "herdr warns about it"
		}
		out = append(out, f)
	}
	summary := Finding{Area: "plugins", Health: Healthy, Summary: fmt.Sprintf("%d installed, none with problems", len(plugins))}
	if len(out) > 0 {
		summary.Summary = fmt.Sprintf("%d installed, %d with problems", len(plugins), len(out))
		summary.Health = Warning
		for _, f := range out {
			if f.Health == Failing {
				summary.Health = Failing
			}
		}
	}
	return append([]Finding{summary}, out...)
}
