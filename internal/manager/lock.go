package manager

import (
	"os"
	"path/filepath"
	"syscall"
)

// lockFile is the file whose lock serialises the changes to the history
// directory's files between processes: the popup and the command line can
// run at the same time.
const lockFile = "lock"

// lock takes the history directory's lock for a read-modify-write of its
// files, waiting while another process holds it.
func (h *History) lock() (unlock func(), err error) {
	if err := os.MkdirAll(h.Dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(h.Dir, lockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
