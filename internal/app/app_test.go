package app

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestStateDirPlatformDefaults(t *testing.T) {
	homePath, local, profile := "/home/test", "/local", "/profile"
	home := func() (string, error) {
		if homePath == "" {
			return "", errors.New("no home")
		}
		return homePath, nil
	}
	xdg := t.TempDir()
	tests := []struct {
		name, goos string
		env        map[string]string
		want       string
	}{
		{"XDG override", "windows", map[string]string{"XDG_STATE_HOME": xdg, "LOCALAPPDATA": "/local"}, filepath.Join(xdg, longName)},
		{"Windows local data", "windows", map[string]string{"LOCALAPPDATA": local, "USERPROFILE": profile}, filepath.Join(local, longName)},
		{"Windows profile", "windows", map[string]string{"USERPROFILE": profile}, filepath.Join(profile, "AppData", "Local", longName)},
		{"Unix home", "linux", map[string]string{"LOCALAPPDATA": local}, filepath.Join(homePath, ".local", "state", longName)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stateDir(tt.goos, func(k string) string { return tt.env[k] }, home)
			if got != tt.want {
				t.Fatalf("stateDir() = %q, want %q", got, tt.want)
			}
		})
	}
	homePath = ""
	if got := stateDir("windows", func(string) string { return "" }, home); got != "" {
		t.Fatalf("no home: got %q, want empty", got)
	}
}
