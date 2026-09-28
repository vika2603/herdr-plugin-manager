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
	Keys map[string][]string `toml:"keys"`
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
