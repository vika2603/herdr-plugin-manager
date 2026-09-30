package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"

	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-plugin-manager/internal/market"
	"github.com/vika2603/herdr-plugin-manager/internal/safe"
	"github.com/vika2603/herdr-plugin-manager/internal/source"
)

// ErrNoReadme is returned when a plugin has no README.
var ErrNoReadme = errors.New("this plugin has no README")

// readmeNames are tried in order in each directory.
var readmeNames = []string{"README.md", "readme.md", "Readme.md", "README.markdown", "README"}

// maxReadme bounds how much of a README is read.
const maxReadme = 1 << 20

// Readme is a plugin's README, as Markdown.
type Readme struct {
	Markdown string
	// Location is the file or URL it was read from.
	Location string
}

// RemoteReadme reads the README of src at commit: the plugin directory's
// own, else the repository's.
func (m *Manager) RemoteReadme(ctx context.Context, src source.GitHub, commit string) (*Readme, error) {
	dirs := []string{src.Subdir}
	if src.Subdir != "" {
		dirs = append(dirs, "")
	}
	for _, dir := range dirs {
		for _, name := range readmeNames {
			p := path.Join(dir, name)
			data, err := m.Market.File(ctx, src, commit, p)
			if errors.Is(err, market.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("read the README of %s: %w", src, err)
			}
			return newReadme(data, src.RawURL(commit, p)), nil
		}
	}
	return nil, ErrNoReadme
}

// InstalledReadme reads the README of an installed plugin from its
// directory, else, for a GitHub install in a subdirectory, from the root of
// the checkout.
func (*Manager) InstalledReadme(p herdr.InstalledPluginInfo) (*Readme, error) {
	dirs := []string{p.PluginRoot}
	if managed := p.Source.ValueOrZero().ManagedPath.ValueOrZero(); managed != "" && managed != p.PluginRoot {
		dirs = append(dirs, managed)
	}
	for _, dir := range dirs {
		for _, name := range readmeNames {
			file := filepath.Join(dir, name)
			data, err := readLimited(file)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			return newReadme(data, file), nil
		}
	}
	return nil, ErrNoReadme
}

func readLimited(file string) ([]byte, error) {
	f, err := os.Open(file) //nolint:gosec // A README inside a plugin directory herdr registered.
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, maxReadme))
}

// newReadme keeps the text printable: a README is the plugin author's and
// is rendered to the terminal.
func newReadme(data []byte, location string) *Readme {
	if len(data) > maxReadme {
		data = data[:maxReadme]
	}
	return &Readme{Markdown: safe.Text(string(data)), Location: location}
}

// OpenURL opens url in the default browser.
func (*Manager) OpenURL(ctx context.Context, url string) error {
	name, args := urlOpener(runtime.GOOS, url)
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // The system's URL opener with a github.com URL built by source.GitHub.
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("open %s: %w: %s", url, err, safe.Line(string(out)))
	}
	return nil
}

func urlOpener(goos, url string) (name string, args []string) {
	switch goos {
	case "darwin":
		return "open", []string{url}
	case "windows":
		return "rundll32.exe", []string{"url.dll,FileProtocolHandler", url}
	default:
		return "xdg-open", []string{url}
	}
}
