//go:build windows

package manager

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestFollowCanBeReplacedWhilePreviousVersionIsOpen(t *testing.T) {
	h := &History{Dir: t.TempDir()}
	first := &Follow{Source: "one/repo", Ref: "main", Commit: "a"}
	if err := h.setFollow("one.plugin", first); err != nil {
		t.Fatal(err)
	}
	reader, err := openHistoryRead(h.followsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	second := &Follow{Source: "two/repo", Ref: "main", Commit: "b"}
	if err := h.setFollow("two.plugin", second); err != nil {
		t.Fatalf("replace follows while the old version is open: %v", err)
	}
	all, err := h.follows()
	if err != nil || all["one.plugin"] != *first || all["two.plugin"] != *second {
		t.Fatalf("follows = %v, %v", all, err)
	}
}

func TestHistoryCanTrimWhilePreviousVersionIsOpen(t *testing.T) {
	h := &History{Dir: t.TempDir()}
	path := filepath.Join(h.Dir, historyFile)
	line := []byte(`{"id":"old","plugin":"one.plugin"}` + "\n")
	if err := os.WriteFile(path, bytes.Repeat(line, historyMax), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := openHistoryRead(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	e := Entry{Kind: KindUpdate, Plugin: "two.plugin"}
	if err := h.add(&e); err != nil {
		t.Fatalf("trim history while the old version is open: %v", err)
	}
	entries, err := h.List()
	if err != nil || len(entries) != historyKeep || entries[len(entries)-1].Plugin != e.Plugin {
		t.Fatalf("trimmed history = %d entries, %v", len(entries), err)
	}
}
