//go:build !windows

package manager

import "os"

func openHistoryRead(path string) (*os.File, error) {
	return os.Open(path) //nolint:gosec // Callers supply a file inside the manager's history directory.
}
