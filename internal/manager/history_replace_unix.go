//go:build !windows

package manager

import "os"

func replaceHistoryFile(replacement, target string) error {
	return os.Rename(replacement, target)
}
