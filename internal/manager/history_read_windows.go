//go:build windows

package manager

import (
	"os"

	"golang.org/x/sys/windows"
)

// openHistoryRead permits a writer in another process to replace the file
// while this reader still holds the previous complete version open.
func openHistoryRead(path string) (*os.File, error) {
	pathp, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := windows.CreateFile(pathp, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}
