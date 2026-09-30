//go:build windows

package manager

import (
	"errors"
	"io/fs"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var replaceFileW = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReplaceFileW")

// replaceHistoryFile replaces an existing file while readers hold handles to
// its previous version. MoveFileEx, used by os.Rename, rejects that case on
// Windows even when those readers allow FILE_SHARE_DELETE.
func replaceHistoryFile(replacement, target string) error {
	if _, err := os.Stat(target); errors.Is(err, fs.ErrNotExist) {
		return os.Rename(replacement, target)
	} else if err != nil {
		return err
	}
	oldPath, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	newPath, err := windows.UTF16PtrFromString(replacement)
	if err != nil {
		return err
	}
	r, _, callErr := replaceFileW.Call(
		uintptr(unsafe.Pointer(oldPath)), uintptr(unsafe.Pointer(newPath)), 0, 0, 0, 0,
	)
	if r == 0 {
		return &os.LinkError{Op: "replace", Old: replacement, New: target, Err: callErr}
	}
	return nil
}
