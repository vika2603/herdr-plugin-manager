//go:build windows

package herdrcli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

const (
	cancelHelperMode = "HPM_CANCEL_HELPER_MODE"
	cancelHelperPID  = "HPM_CANCEL_HELPER_PID_FILE"
)

// This helper runs in two child processes so the test can check that
// cancelling herdr also terminates a command it launched.
func TestCancelStopsWhatHerdrRuns(t *testing.T) {
	switch os.Getenv(cancelHelperMode) {
	case "child":
		if err := os.WriteFile(os.Getenv(cancelHelperPID), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Second)
		return
	case "parent":
		child := exec.Command(os.Args[0], "-test.run=^TestCancelStopsWhatHerdrRuns$")
		child.Env = append(os.Environ(), cancelHelperMode+"=child")
		if err := child.Run(); err != nil {
			t.Fatal(err)
		}
		return
	}

	pidFile := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCancelStopsWhatHerdrRuns$")
	cmd.Env = append(os.Environ(), cancelHelperMode+"=parent", cancelHelperPID+"="+pidFile)
	g := newGroupCmd(cmd)
	done := make(chan error, 1)
	go func() { done <- g.Run() }()

	var pid int
	deadline := time.Now().Add(5 * time.Second)
	for pid == 0 && time.Now().Before(deadline) {
		data, _ := os.ReadFile(pidFile)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		if pid == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if pid == 0 {
		cancel()
		select {
		case err := <-done:
			t.Fatalf("build child did not start: %v", err)
		case <-time.After(10 * time.Second):
			t.Fatal("herdr helper did not stop")
		}
	}
	defer terminateTestProcess(pid)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled herdr command exited successfully")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled herdr command did not stop")
	}
	deadline = time.Now().Add(2 * time.Second)
	for processExists(pid) {
		if time.Now().After(deadline) {
			t.Fatal("the build command herdr launched is still running")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func processExists(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code == 259 // STILL_ACTIVE
}

func terminateTestProcess(pid int) {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return
	}
	defer func() { _ = windows.CloseHandle(h) }()
	_ = windows.TerminateProcess(h, 1)
}
