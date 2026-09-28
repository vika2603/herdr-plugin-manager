// Package app builds the manager for the two ways the binary runs: as a herdr
// plugin, where herdr injects the socket and binary paths, and as a
// standalone command, where they are resolved the way the herdr command
// resolves them.
package app

import (
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
const Version = "0.1.1"

// Name is the command name.
const Name = "hpm"

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
	return &manager.Manager{
		Market:   market.NewClient(cacheDir(), longName+"/"+Version),
		Git:      updates.Git{},
		Platform: compat.Platform(),
	}
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
