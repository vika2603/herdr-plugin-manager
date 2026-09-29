package manager

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/vika2603/herdr-client/herdr"
)

// Usage is how an installed plugin is used: where herdr keeps its config,
// the actions a key can run, and the keys herdr's config binds to them.
type Usage struct {
	// ConfigDir is the directory herdr gives the plugin for its config, or
	// ConfigErr why it could not be read.
	ConfigDir string
	ConfigErr error
	Actions   []UsageAction
	// Panes are the panes the plugin opens, by id and title.
	Panes []string
	// HerdrConfig is herdr's config file, where keys are bound; KeysErr is
	// why its key bindings could not be read.
	HerdrConfig string
	KeysErr     error
}

// UsageAction is an action a key can run.
type UsageAction struct {
	ID, Title string
	// Command is what a binding runs it by: the plugin id and the action
	// id, which is unique where the action id alone may not be.
	Command string
	// Keys are the keys herdr's config binds to it.
	Keys []string
}

// Snippet is a binding of action to a key for herdr's config, with the key
// left to choose.
func (a UsageAction) Snippet() []string {
	return []string{
		"[[keys.command]]",
		`key = "prefix+..."  # a key not bound yet`,
		`type = "plugin_action"`,
		fmt.Sprintf("command = %q", a.Command),
		fmt.Sprintf("description = %q", a.Title),
	}
}

// Usage describes how to use installed plugin p.
func (m *Manager) Usage(ctx context.Context, p herdr.InstalledPluginInfo) Usage {
	u := Usage{HerdrConfig: HerdrConfigPath()}
	u.ConfigDir, u.ConfigErr = m.CLI.ConfigDir(ctx, p.PluginID)
	bindings, err := keyBindings(u.HerdrConfig)
	u.KeysErr = err
	for _, a := range p.Actions.ValueOrZero() {
		ua := UsageAction{ID: a.ID, Title: a.Title, Command: p.PluginID + "." + a.ID}
		for _, b := range bindings {
			if b.Type == "plugin_action" && (b.Command == ua.Command || b.Command == a.ID) {
				ua.Keys = append(ua.Keys, b.Key)
			}
		}
		u.Actions = append(u.Actions, ua)
	}
	for _, pn := range p.Panes.ValueOrZero() {
		u.Panes = append(u.Panes, pn.ID+": "+pn.Title)
	}
	return u
}

// Sections lays the usage out for the command line.
func (u Usage) Sections() []Section {
	config := u.ConfigDir
	if u.ConfigErr != nil {
		config = "not known: " + u.ConfigErr.Error()
	}
	out := []Section{{Title: "Use", Lines: []string{"config directory: " + config}}}
	if len(u.Actions) == 0 {
		out[0].Lines = append(out[0].Lines, "actions: none, so no key can run it")
	}
	var unbound *UsageAction
	for i, a := range u.Actions {
		line := fmt.Sprintf("action %s: %s, run as %s", a.ID, a.Title, a.Command)
		if len(a.Keys) > 0 {
			line += "; bound to " + strings.Join(a.Keys, ", ")
		} else if unbound == nil {
			unbound = &u.Actions[i]
		}
		out[0].Lines = append(out[0].Lines, line)
	}
	for _, pn := range u.Panes {
		out[0].Lines = append(out[0].Lines, "pane "+pn)
	}
	if u.KeysErr != nil {
		out[0].Lines = append(out[0].Lines, "key bindings: not known: "+u.KeysErr.Error())
	}
	if unbound != nil {
		out = append(out, Section{Title: "Bind a key", Note: "in " + u.HerdrConfig + ", then herdr server reload-config", Lines: unbound.Snippet()})
	}
	return out
}

// HerdrConfigPath is herdr's config file: $HERDR_CONFIG_PATH, else
// config.toml in $XDG_CONFIG_HOME/herdr or ~/.config/herdr, as herdr 0.9
// resolves it.
func HerdrConfigPath() string {
	if p := os.Getenv("HERDR_CONFIG_PATH"); p != "" {
		return p
	}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "herdr", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("~", ".config", "herdr", "config.toml")
	}
	return filepath.Join(home, ".config", "herdr", "config.toml")
}

// KeyBinding is a [[keys.command]] entry of herdr's config.
type KeyBinding struct {
	Key         string `toml:"key"`
	Type        string `toml:"type"`
	Command     string `toml:"command"`
	Description string `toml:"description"`
}

// keyBindings reads the custom command keys of herdr's config at path; none
// when it does not exist.
func keyBindings(path string) ([]KeyBinding, error) {
	var cfg struct {
		Keys struct {
			Command []KeyBinding `toml:"command"`
		} `toml:"keys"`
	}
	_, err := toml.DecodeFile(path, &cfg)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return cfg.Keys.Command, nil
}
