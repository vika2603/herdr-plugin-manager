//go:build !windows

package herdrcli

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// killDelay gives a cancelled command's process group time to exit after an
// interrupt before it is killed.
const killDelay = 5 * time.Second

func newGroupCmd(cmd *exec.Cmd) *groupCmd {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	g := &groupCmd{Cmd: cmd}
	cmd.Cancel = g.interrupt
	cmd.WaitDelay = 10 * time.Second
	return g
}

// interrupt reaches both herdr and the build commands it starts.
func (g *groupCmd) interrupt() error {
	pgid := g.Process.Pid
	time.AfterFunc(killDelay, func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		if !g.exited {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
	})
	return syscall.Kill(-pgid, syscall.SIGINT)
}

func processSignal(exitErr *exec.ExitError) os.Signal {
	if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return ws.Signal()
	}
	return nil
}
