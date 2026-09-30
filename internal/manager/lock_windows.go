//go:build windows

package manager

import (
	"os"

	"golang.org/x/sys/windows"
)

func lockHistoryFile(f *os.File) (func(), error) {
	// The lock covers one byte; the file is never otherwise read or written.
	// LockFileEx without LOCKFILE_FAIL_IMMEDIATELY waits for another process.
	handle := windows.Handle(f.Fd())
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped); err != nil {
		return nil, err
	}
	return func() { _ = windows.UnlockFileEx(handle, 0, 1, 0, &overlapped) }, nil
}
