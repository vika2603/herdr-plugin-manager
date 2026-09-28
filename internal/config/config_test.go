package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	if c, err := Load(t.TempDir()); err != nil || c.Keys != nil {
		t.Errorf("a missing file: %+v, %v; want the zero config", c, err)
	}
	dir := t.TempDir()
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, File), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("[keys]\ninstall = [\"I\"]\nsort = []\n")
	c, err := Load(dir)
	if err != nil || !slices.Equal(c.Keys["install"], []string{"I"}) || c.Keys["sort"] == nil {
		t.Errorf("keys = %v, %v", c.Keys, err)
	}
	write("[keys]\ninstall = \"I\"\n")
	if _, err := Load(dir); err == nil {
		t.Error("a key that is not a list should fail")
	}
	write("[key]\ninstall = [\"I\"]\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "unknown setting key") {
		t.Errorf("a misspelt table: err = %v", err)
	}
}
