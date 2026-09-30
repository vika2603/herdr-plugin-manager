package manager

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vika2603/herdr-plugin-manager/internal/safe"
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
// JSON object per line. The popup and the command line share it, so its
// files change only under the directory's lock.
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
	// Error is why the change failed, empty when it succeeded; Cancelled is
	// set when it was stopped.
	Error     string `json:"error,omitempty"`
	Cancelled bool   `json:"cancelled,omitempty"`
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

// StateChange is the plugin's state before and after the change, on one
// line.
func (e Entry) StateChange() string {
	return stateLabel(e.Before, false) + " -> " + stateLabel(e.After, e.AfterUnknown)
}

// Details are the change's target, the plugin's state before and after it,
// and its error, a line each.
func (e Entry) Details() []string {
	var out []string
	if e.Target != nil {
		out = append(out, "target: "+e.Target.Source+" @ "+RevisionLabel(e.Target.Ref, e.Target.Commit))
	}
	out = append(out, "before: "+stateLabel(e.Before, false), "after: "+stateLabel(e.After, e.AfterUnknown))
	if e.Failed() {
		out = append(out, "error: "+safe.Text(e.Error))
	}
	return out
}

func stateLabel(s *State, unknown bool) string {
	switch {
	case unknown:
		return "unknown"
	case s == nil:
		return "not installed"
	}
	return s.String()
}

// Result is how the change ended, in a word: done, failed, cancelled, or
// unconfirmed when the plugin's state afterwards is not known.
func (e Entry) Result() string {
	switch {
	case e.AfterUnknown:
		return "unconfirmed"
	case e.Cancelled:
		return "cancelled"
	case e.Failed():
		return "failed"
	}
	return "done"
}

// add appends e to the log, giving it an id and a time when it has none.
// An entry without an id gets one, and the empty log file that holds it.
func (h *History) add(e *Entry) error {
	if h == nil || h.Dir == "" {
		return nil
	}
	if e.ID == "" {
		if f := h.reserve(e); f != nil {
			_ = f.Close()
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	unlock, err := h.lock()
	if err != nil {
		return err
	}
	defer unlock()
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

// idTries bounds the ids tried for one moment before falling back to one
// with the process id.
const idTries = 1000

var fallbackSeq atomic.Int64

// reserve gives e its time, unless it has one, and an id no other entry
// has, even one another process records in the same millisecond. The id is
// taken by creating the entry's log file, which only one process can do;
// the file is returned open for the change's output. When no log file can
// be created, it returns nil and the id carries the process id instead.
func (h *History) reserve(e *Entry) *os.File {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	base := e.Time.UTC().Format("20060102T150405.000")
	if err := os.MkdirAll(h.logDir(), 0o700); err == nil {
		for seq := range idTries {
			id := fmt.Sprintf("%s-%d", base, seq)
			path := filepath.Join(h.logDir(), id+".log")
			f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // A new file named by the entry id in the history directory.
			if errors.Is(err, fs.ErrExist) {
				continue
			}
			if err != nil {
				break
			}
			e.ID, e.Log = id, path
			return f
		}
	}
	e.ID = fmt.Sprintf("%s-p%d.%d", base, os.Getpid(), fallbackSeq.Add(1))
	return nil
}

// trim cuts the log back once it has grown past historyMax entries,
// dropping their output files with them.
func (h *History) trim(path string) error {
	data, err := readHistoryFile(path)
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
	if err := os.WriteFile(tmp, kept, 0o600); err != nil {
		return err
	}
	return replaceHistoryFile(tmp, path)
}

// List returns the recorded changes, the oldest first. Lines that cannot be
// read, such as one cut short, are skipped.
func (h *History) List() ([]Entry, error) {
	if h == nil || h.Dir == "" {
		return nil, nil
	}
	f, err := openHistoryRead(filepath.Join(h.Dir, historyFile))
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

func readHistoryFile(path string) ([]byte, error) {
	f, err := openHistoryRead(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}
