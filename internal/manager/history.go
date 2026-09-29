package manager

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// historyFile is the change log's name in the history directory.
const historyFile = "history.jsonl"

// History limits: once the log holds historyMax entries it is cut back to
// the newest historyKeep.
const (
	historyMax  = 1000
	historyKeep = 500
)

// History is the log of changes the manager made to installed plugins, one
// JSON object per line. The popup and the command line share it, so each
// entry is appended with a single write.
type History struct {
	// Dir holds the log; empty keeps no history.
	Dir string
	mu  sync.Mutex
}

// Entry is one recorded change.
type Entry struct {
	// ID identifies the entry in the log: its time with a sequence suffix.
	ID     string     `json:"id"`
	Time   time.Time  `json:"time"`
	Kind   ChangeKind `json:"kind"`
	Plugin string     `json:"plugin"`
	// Target is what herdr was asked to install; nil for an uninstall.
	Target *TargetRecord `json:"target,omitempty"`
	// Before and After are the plugin's records around the change, nil when
	// it was not installed. AfterUnknown is set when the record could not be
	// read afterwards.
	Before       *State `json:"before,omitempty"`
	After        *State `json:"after,omitempty"`
	AfterUnknown bool   `json:"after_unknown,omitempty"`
	// Error is why the change failed, empty when it succeeded.
	Error string `json:"error,omitempty"`
	// Log is the file holding everything herdr printed during the change.
	Log string `json:"log,omitempty"`
}

// TargetRecord is the recorded form of a Target.
type TargetRecord struct {
	Source string `json:"source"`
	Ref    string `json:"ref,omitempty"`
	Commit string `json:"commit,omitempty"`
}

// Failed reports whether the change failed.
func (e Entry) Failed() bool { return e.Error != "" }

// add appends e to the log, giving it an id and a time when it has none.
func (h *History) add(e *Entry) error {
	if h == nil || h.Dir == "" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	if e.ID == "" {
		e.ID = entryID(e.Time)
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(h.Dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(h.Dir, historyFile)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // The log in the history directory.
	if err != nil {
		return err
	}
	_, werr := f.Write(append(line, '\n'))
	if err := errors.Join(werr, f.Close()); err != nil {
		return err
	}
	return h.trim(path)
}

var (
	idMu   sync.Mutex
	lastID string
	idSeq  int
)

// entryID is unique within this process and sorts by time.
func entryID(t time.Time) string {
	idMu.Lock()
	defer idMu.Unlock()
	base := t.UTC().Format("20060102T150405.000")
	if base == lastID {
		idSeq++
	} else {
		lastID, idSeq = base, 0
	}
	return fmt.Sprintf("%s-%d", base, idSeq)
}

// trim cuts the log back once it has grown past historyMax entries,
// dropping their output files with them.
func (h *History) trim(path string) error {
	data, err := os.ReadFile(path) //nolint:gosec // The log in the history directory.
	if err != nil {
		return err
	}
	lines := bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n"))
	if len(lines) <= historyMax {
		return nil
	}
	for _, line := range lines[:len(lines)-historyKeep] {
		var e Entry
		if json.Unmarshal(line, &e) == nil && e.Log != "" && filepath.Dir(e.Log) == h.logDir() {
			_ = os.Remove(e.Log)
		}
	}
	kept := append(bytes.Join(lines[len(lines)-historyKeep:], []byte("\n")), '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, kept, 0o600); err != nil { //nolint:gosec // Beside the log in the history directory.
		return err
	}
	return os.Rename(tmp, path)
}

// List returns the recorded changes, the oldest first. Lines that cannot be
// read, such as one cut short, are skipped.
func (h *History) List() ([]Entry, error) {
	if h == nil || h.Dir == "" {
		return nil, nil
	}
	f, err := os.Open(filepath.Join(h.Dir, historyFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.ID != "" {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// Find returns the entry with id.
func (h *History) Find(id string) (Entry, bool, error) {
	list, err := h.List()
	if err != nil {
		return Entry{}, false, err
	}
	i := slices.IndexFunc(list, func(e Entry) bool { return e.ID == id })
	if i < 0 {
		return Entry{}, false, nil
	}
	return list[i], true, nil
}

// LastChange returns the newest entry for plugin id that changed it. A
// failed change that left the plugin as it was is passed over.
func (h *History) LastChange(id string) (Entry, bool, error) {
	list, err := h.List()
	if err != nil {
		return Entry{}, false, err
	}
	for _, e := range slices.Backward(list) {
		if e.Plugin == id && e.changed() {
			return e, true, nil
		}
	}
	return Entry{}, false, nil
}

func (e Entry) changed() bool {
	switch {
	case e.AfterUnknown:
		return true
	case e.Before == nil || e.After == nil:
		return e.Before != e.After
	}
	return !e.Before.SameRevision(*e.After) || e.Before.Enabled != e.After.Enabled
}

func (h *History) logDir() string { return filepath.Join(h.Dir, "logs") }
