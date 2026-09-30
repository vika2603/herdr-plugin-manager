package manager

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-plugin-manager/internal/source"
)

// followsFile keeps, beside the history, the refs the plugins this manager
// pinned follow.
const followsFile = "follows.json"

// Follow is the ref a plugin follows although herdr records it as a commit
// pin. herdr fetches the ref it is given and builds what it fetched, so an
// install that must build the commit a preview showed asks herdr for that
// commit; herdr then records the commit as the plugin's ref, and the ref it
// follows is kept here.
type Follow struct {
	Source string `json:"source"`
	// Ref is the ref followed, empty for the default branch, and Commit the
	// pin herdr recorded for it.
	Ref    string `json:"ref"`
	Commit string `json:"commit"`
	// Installed is when herdr recorded the install, which it sets anew on
	// every install; 0 when herdr did not say.
	Installed uint64 `json:"installed_unix_ms,omitempty"`
}

func (h *History) followsPath() string { return filepath.Join(h.Dir, followsFile) }

// follows reads the kept refs by plugin id.
func (h *History) follows() (map[string]Follow, error) {
	out := map[string]Follow{}
	if h == nil || h.Dir == "" {
		return out, nil
	}
	data, err := readHistoryFile(h.followsPath())
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return out, nil
	case err != nil:
		return nil, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// setFollow keeps f for plugin id, or with f nil forgets what id follows.
func (h *History) setFollow(id string, f *Follow) error {
	if h == nil || h.Dir == "" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	unlock, err := h.lock()
	if err != nil {
		return err
	}
	defer unlock()
	all, err := h.follows()
	if err != nil {
		return err
	}
	if f == nil {
		if _, ok := all[id]; !ok {
			return nil
		}
		delete(all, id)
	} else {
		all[id] = *f
	}
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	tmp := h.followsPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return replaceHistoryFile(tmp, h.followsPath())
}

// applyFollows gives each plugin that is still at the pin this manager made
// the ref it follows. A plugin herdr now records otherwise, as after an
// install outside this manager, keeps herdr's record; so does one installed
// again at the same pin, which herdr records with a new install time.
func applyFollows(plugins []herdr.InstalledPluginInfo, follows map[string]Follow) {
	for i := range plugins {
		p := &plugins[i]
		f, ok := follows[p.PluginID]
		src, github := source.FromInstalled(*p)
		info := p.Source.ValueOrZero()
		if !ok || !github || src.String() != f.Source ||
			info.RequestedRef.ValueOrZero() != f.Commit || info.ResolvedCommit.ValueOrZero() != f.Commit ||
			f.Installed != 0 && info.InstalledUnixMs.ValueOrZero() != f.Installed {
			continue
		}
		if f.Ref == "" {
			info.RequestedRef = herdr.Optional[string]{}
		} else {
			info.RequestedRef = herdr.Some(f.Ref)
		}
		p.Source = herdr.Some(info)
	}
}
