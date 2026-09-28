// Package config reads the manager's config.toml, kept in the config
// directory herdr gives the plugin.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// File is the config file's name inside the plugin config directory.
const File = "config.toml"

// Config is the config file.
type Config struct {
	// Keys replaces the keys of actions, by action name; `hpm keys` lists
	// them.
	Keys  map[string][]string `toml:"keys"`
	Theme Theme               `toml:"theme"`
}

// Theme picks the interface's colours; docs/design.md names the roles.
type Theme struct {
	// Accent is indigo, teal, magenta or a #RRGGBB colour.
	Accent string `toml:"accent"`
	// Mode is auto, which follows the terminal's background, dark or light.
	Mode string `toml:"mode"`
	// Dark and Light replace colours by role on each background.
	Dark  map[string]string `toml:"dark"`
	Light map[string]string `toml:"light"`
}

// Load reads the config file in dir. A missing file, or no dir, is the
// zero Config.
func Load(dir string) (Config, error) {
	var c Config
	if dir == "" {
		return c, nil
	}
	path := filepath.Join(dir, File)
	md, err := toml.DecodeFile(path, &c)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Config{}, nil
	case err != nil:
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	if extra := md.Undecoded(); len(extra) > 0 {
		return c, fmt.Errorf("%s: unknown setting %s", path, extra[0])
	}
	return c, nil
}
