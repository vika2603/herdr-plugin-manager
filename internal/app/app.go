// Package app builds the manager for the two ways the binary runs: as a herdr
// plugin, where herdr injects the socket and binary paths, and as a
// standalone command, where they are resolved the way the herdr command
// resolves them.
package app

import (
	"cmp"
	"context"
	"os"
	"path/filepath"

	"github.com/vika2603/herdr-client/herdr"
	"github.com/vika2603/herdr-client/plugin"

	"github.com/vika2603/herdr-plugin-manager/internal/compat"
	"github.com/vika2603/herdr-plugin-manager/internal/herdrcli"
	"github.com/vika2603/herdr-plugin-manager/internal/manager"
	"github.com/vika2603/herdr-plugin-manager/internal/market"
	"github.com/vika2603/herdr-plugin-manager/internal/updates"
)

// Version is the manager's version, kept equal to herdr-plugin.toml.
const Version = "0.1.3"

// Name is the command name.
const Name = "hpm"

// PluginID is the id herdr-plugin.toml declares.
const PluginID = "vika2603.plugin-manager"

// longName names the cache directory and the HTTP user agent, where the
// short command name could be mistaken for another tool's.
const longName = "herdr-plugin-manager"

// ForPlugin builds the manager for a plugin entrypoint.
func ForPlugin(env *plugin.Env) *manager.Manager {
	m := base()
	m.API = env.Client()
	m.CLI = herdrcli.Runner{Bin: env.BinPath}
	m.SelfID = env.PluginID
	return m
}

// Standalone builds the manager for the command line. The API client is left
// nil when no socket path can be resolved; operations that need a server then
// report that none is running.
func Standalone() *manager.Manager {
	m := base()
	if client, err := herdr.NewFromEnv(); err == nil {
		m.API = client
	}
	m.CLI = herdrcli.Runner{Bin: os.Getenv("HERDR_BIN_PATH")}
	return m
}

func base() *manager.Manager {
	mc := market.NewClient(cacheDir(), longName+"/"+Version)
	mc.Token = cmp.Or(os.Getenv("GH_TOKEN"), os.Getenv("GITHUB_TOKEN"))
	return &manager.Manager{
		Market:   mc,
		Git:      updates.Git{},
		Platform: compat.Platform(),
		History:  &manager.History{Dir: StateDir()},
	}
}

// StateDir holds the change history, shared by the popup and the command
// line: $XDG_STATE_HOME/herdr-plugin-manager, or ~/.local/state/... without
// it. herdr gives only a plugin entrypoint its state directory, so the
// command line could not find that one. Empty keeps no history.
func StateDir() string {
	if dir := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, longName)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", longName)
}

// cacheDir is shared by the popup and the command line, so either one reuses
// the index the other downloaded. Empty disables the cache.
func cacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, longName)
}

// ConfigDir is the plugin's config directory as herdr reports it, where the
// popup reads its config too; "" when herdr cannot say.
func ConfigDir(ctx context.Context, m *manager.Manager) string {
	dir, err := m.CLI.ConfigDir(ctx, PluginID)
	if err != nil {
		return ""
	}
	return dir
}
