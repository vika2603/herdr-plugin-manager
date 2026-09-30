package manager

import (
	"slices"
	"testing"
)

func TestURLOpenerUsesPlatformCommandWithoutShell(t *testing.T) {
	url := "https://example.com/a?x=1&y=2"
	for _, tt := range []struct {
		goos, name string
		args       []string
	}{
		{"darwin", "open", []string{url}},
		{"linux", "xdg-open", []string{url}},
		{"windows", "rundll32.exe", []string{"url.dll,FileProtocolHandler", url}},
	} {
		name, args := urlOpener(tt.goos, url)
		if name != tt.name || !slices.Equal(args, tt.args) {
			t.Errorf("%s: got %q %q, want %q %q", tt.goos, name, args, tt.name, tt.args)
		}
	}
}
