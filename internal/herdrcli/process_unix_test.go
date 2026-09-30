//go:build !windows

package herdrcli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCancelStopsWhatHerdrRuns(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "build.pid")
	// A build herdr runs in the foreground and waits for, as it does a build
	// command.
	r, _ := fakeHerdr(t, "sh -c 'echo $$ > "+pidFile+"; exec sleep 30'")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Install(ctx, "o/r", "", nil) }()
	var pid int
	for pid == 0 {
		data, _ := os.ReadFile(pidFile)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err == nil || !strings.Contains(err.Error(), "was stopped by signal interrupt") {
		t.Fatalf("err = %v, want the install stopped by the interrupt", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatal("the build herdr ran was still running after the install was cancelled")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
