// Package herdrcli runs the herdr command for the plugin operations the socket
// API does not offer: installing and uninstalling GitHub plugins, and listing
// plugins while no herdr server is running.
package herdrcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/vika2603/herdr-client/herdr"
)

// Runner runs one herdr binary.
type Runner struct {
	// Bin is the herdr executable; empty means "herdr" on PATH.
	Bin string
}

func (r Runner) bin() string {
	if r.Bin == "" {
		return "herdr"
	}
	return r.Bin
}

// Install runs `herdr plugin install src [--ref ref] --yes`, writing herdr's
// output to out. herdr clones the repository, runs the manifest's build
// commands and registers the plugin; the preview it would show interactively
// is expected to have been shown by the caller. herdr records ref as given,
// so a commit is recorded as a pin. A reinstall replaces the managed checkout
// and registers the plugin as enabled.
func (r Runner) Install(ctx context.Context, src, ref string, out io.Writer) error {
	args := []string{"plugin", "install", src}
	if ref != "" {
		args = append(args, "--ref", ref)
	}
	return r.run(ctx, append(args, "--yes"), out)
}

// Uninstall runs `herdr plugin uninstall id`, which unregisters the plugin
// and, for a GitHub install, removes its managed checkout.
func (r Runner) Uninstall(ctx context.Context, id string, out io.Writer) error {
	return r.run(ctx, []string{"plugin", "uninstall", id}, out)
}

// List runs `herdr plugin list --json`. Unlike the socket API it works while
// no server is running, by reading herdr's plugin registry.
func (r Runner) List(ctx context.Context) ([]herdr.InstalledPluginInfo, error) {
	var stdout, stderr bytes.Buffer
	cmd := r.command(ctx, []string{"plugin", "list", "--json"})
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, commandError(cmd.Args, err, stderr.String())
	}
	result, err := decodeResponse(stdout.Bytes())
	if err != nil {
		return nil, err
	}
	list, ok := result.(*herdr.PluginListResponse)
	if !ok {
		return nil, fmt.Errorf("herdr plugin list: unexpected %q result", result.ResultType())
	}
	return list.Plugins, nil
}

// Version returns the version `herdr --version` prints, such as "0.9.1".
func (r Runner) Version(ctx context.Context) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := r.command(ctx, []string{"--version"})
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", commandError(cmd.Args, err, stderr.String())
	}
	fields := strings.Fields(stdout.String())
	if len(fields) < 2 || fields[0] != "herdr" {
		return "", fmt.Errorf("unexpected herdr --version output %q", strings.TrimSpace(stdout.String()))
	}
	return fields[1], nil
}

// ConfigDir is the directory herdr keeps plugin id's configuration in,
// which it hands the plugin itself as HERDR_PLUGIN_CONFIG_DIR.
func (r Runner) ConfigDir(ctx context.Context, id string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := r.command(ctx, []string{"plugin", "config-dir", id})
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", commandError(cmd.Args, err, stderr.String())
	}
	dir := strings.TrimSpace(stdout.String())
	if dir == "" || strings.Contains(dir, "\n") {
		return "", fmt.Errorf("unexpected herdr plugin config-dir output %q", dir)
	}
	return dir, nil
}

func (r Runner) run(ctx context.Context, args []string, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	// herdr's messages are the useful part of a failure, so they are kept
	// for the returned error as well as written to out.
	var tail bytes.Buffer
	w := io.MultiWriter(out, &tail)
	cmd := r.command(ctx, args)
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Run(); err != nil {
		return commandError(cmd.Args, err, lastLines(tail.String(), 5))
	}
	return nil
}

// killDelay is how long a cancelled herdr command and the build it runs have
// to stop after the interrupt before they are killed.
const killDelay = 5 * time.Second

// command is herdr with args, run in a process group of its own. herdr
// neither handles an interrupt nor passes it on, so on cancellation the
// whole group is interrupted, which reaches the build commands herdr runs,
// and killed once killDelay passes. The group also keeps a terminal's
// ctrl+c from reaching herdr before this process decides what to do.
func (r Runner) command(ctx context.Context, args []string) *groupCmd {
	cmd := exec.CommandContext(ctx, r.bin(), args...) //nolint:gosec // The herdr binary herdr itself names, or herdr on PATH, with fixed subcommands.
	cmd.Env = commandEnv(os.Environ())
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	g := &groupCmd{Cmd: cmd}
	cmd.Cancel = g.interrupt
	cmd.WaitDelay = 10 * time.Second
	return g
}

// groupCmd is a command leading its own process group.
type groupCmd struct {
	*exec.Cmd
	mu     sync.Mutex
	exited bool
}

// interrupt sends the group an interrupt, and a kill after killDelay unless
// the command has exited by then.
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

// Run runs the command. Once it has exited its process group id may be
// reused, so no kill is sent after that.
func (g *groupCmd) Run() error {
	err := g.Cmd.Run()
	g.mu.Lock()
	g.exited = true
	g.mu.Unlock()
	return err
}

// commandEnv drops the plugin invocation variables herdr gave this process.
// They describe the manager's own entrypoint, and would otherwise reach the
// build commands of the plugin being installed. The socket and session
// variables stay so the command talks to the same server.
func commandEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		if !strings.HasPrefix(kv, "HERDR_PLUGIN_") {
			out = append(out, kv)
		}
	}
	return out
}

// ExitError is a herdr command that ran and failed: it exited with Code, or
// a signal stopped it.
type ExitError struct {
	Args   []string
	Code   int
	Signal os.Signal
	Output string
}

func (e *ExitError) Error() string {
	cmd := strings.Join(e.Args, " ")
	if e.Signal != nil {
		// What herdr printed up to the signal is not why it stopped.
		return fmt.Sprintf("%s was stopped by signal %s", cmd, e.Signal)
	}
	msg := fmt.Sprintf("%s exited with status %d", cmd, e.Code)
	if e.Output != "" {
		msg += ": " + e.Output
	}
	return msg
}

func commandError(args []string, err error, output string) error {
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		e := &ExitError{Args: args, Code: exitErr.ExitCode(), Output: strings.TrimSpace(output)}
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			e.Signal = ws.Signal()
		}
		return e
	}
	return fmt.Errorf("run %s: %w", strings.Join(args, " "), err)
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// decodeResponse reads the response envelope herdr prints for --json.
func decodeResponse(data []byte) (herdr.Result, error) {
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *herdr.Error    `json:"error"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("decode herdr output: %w", err)
	}
	if envelope.Error != nil {
		return nil, envelope.Error
	}
	if len(envelope.Result) == 0 {
		return nil, errors.New("herdr output has no result")
	}
	return herdr.DecodeResult(envelope.Result)
}
