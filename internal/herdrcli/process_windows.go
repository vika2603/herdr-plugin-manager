//go:build windows

package herdrcli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func newGroupCmd(cmd *exec.Cmd) *groupCmd {
	// Separate herdr from the popup's console. Its build commands remain
	// descendants, so taskkill /T can stop them together on cancellation.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
	}
	g := &groupCmd{Cmd: cmd}
	cmd.Cancel = g.interrupt
	cmd.WaitDelay = 10 * time.Second
	return g
}

func (g *groupCmd) interrupt() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.exited {
		return os.ErrProcessDone
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "taskkill.exe", "/T", "/F", "/PID", strconv.Itoa(g.Process.Pid)) //nolint:gosec // Only the PID of the herdr process started above is passed to the Windows process-tree terminator.
	if out, err := cmd.CombinedOutput(); err != nil {
		// Even if taskkill is unavailable, the parent must not remain running.
		_ = g.Process.Kill()
		return fmt.Errorf("stop herdr process tree: %w: %s", err, out)
	}
	return nil
}

func processSignal(*exec.ExitError) os.Signal { return nil }
