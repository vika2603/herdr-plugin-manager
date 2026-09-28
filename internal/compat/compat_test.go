package compat

import "testing"

func TestProblems(t *testing.T) {
	tests := []struct {
		name      string
		platforms []string
		min       string
		herdr     string
		want      int
	}{
		{"compatible", []string{"linux", "macos"}, "0.9.0", "0.9.1", 0},
		{"undeclared platforms", nil, "", "0.9.1", 0},
		{"wrong platform", []string{"windows"}, "0.9.0", "0.9.1", 1},
		{"herdr too old", []string{"macos"}, "0.10.0", "0.9.1", 1},
		{"prerelease herdr is older than the release", nil, "0.9.1", "0.9.1-preview.1", 1},
		{"unknown herdr version", nil, "9.9.9", "", 0},
		{"both", []string{"windows"}, "1.0.0", "v0.9.1", 2},
	}
	for _, tt := range tests {
		if got := Problems(tt.platforms, tt.min, tt.herdr, "macos"); len(got) != tt.want {
			t.Errorf("%s: Problems = %q, want %d problems", tt.name, got, tt.want)
		}
	}
}
