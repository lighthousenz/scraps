package actor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/lighthousenz/scraps/internal/protocol"
)

// Process manages a single actor process.
type Process struct {
	cmd       *exec.Cmd
	client    *protocol.Client
	logFile   *os.File
	logMu     sync.Mutex
	exited    chan struct{}
	stdinPipe io.WriteCloser

	// exitErr holds the error from cmd.Wait, if any.
	exitErr error
	// stderrDone is closed when captureStderr finishes reading.
	stderrDone chan struct{}
}

// StartConfig holds the configuration for starting an actor process.
type StartConfig struct {
	// Tools from the actor definition (name -> version).
	Tools map[string]string
	// Run is the shell command to execute.
	Run string
	// Dir is the working directory (the prepared directory).
	Dir string
	// Workdir is the SCRAPS_WORKDIR path (absolute).
	Workdir string
	// Artefacts is the SCRAPS_ARTEFACTS path (absolute).
	Artefacts string
	// LogPath is the path to the log file for stderr and notifications.
	LogPath string
	// OnLog is the callback for scraps/log notifications.
	OnLog protocol.LogHandler
}

// Start spawns an actor process and returns a handle to it.
func Start(ctx context.Context, cfg *StartConfig) (*Process, error) {
	// Ensure workdir and artefacts directories exist.
	if err := os.MkdirAll(cfg.Workdir, 0o755); err != nil {
		return nil, fmt.Errorf("create workdir: %w", err)
	}
	if err := os.MkdirAll(cfg.Artefacts, 0o755); err != nil {
		return nil, fmt.Errorf("create artefacts dir: %w", err)
	}

	name, args, err := BuildCommand(cfg.Tools, cfg.Run)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = cfg.Dir
	cmd.Env = append(os.Environ(),
		"SCRAPS_WORKDIR="+cfg.Workdir,
		"SCRAPS_ARTEFACTS="+cfg.Artefacts,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// On context cancellation, send SIGTERM to the process group (not SIGKILL
	// to the leader), then allow WaitDelay for graceful exit.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = 5 * time.Second

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("create stderr pipe: %w", err)
	}

	// Open log file.
	if err := os.MkdirAll(filepath.Dir(cfg.LogPath), 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	logFile, err := os.Create(cfg.LogPath)
	if err != nil {
		return nil, fmt.Errorf("create log file: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("start actor process: %w", err)
	}

	p := &Process{
		cmd:        cmd,
		logFile:    logFile,
		exited:     make(chan struct{}),
		stdinPipe:  stdinPipe,
		stderrDone: make(chan struct{}),
	}

	// Wrap the log handler to also write to the log file.
	logHandler := func(level, message string) {
		p.logMu.Lock()
		_, _ = fmt.Fprintf(p.logFile, "[%s] %s\n", level, message)
		p.logMu.Unlock()
		if cfg.OnLog != nil {
			cfg.OnLog(level, message)
		}
	}

	p.client = protocol.NewClient(stdinPipe, stdoutPipe, logHandler)

	// Capture stderr.
	go func() {
		p.captureStderr(stderrPipe)
		close(p.stderrDone)
	}()

	// Start the protocol reader.
	go func() {
		p.client.Run()
	}()

	// Wait for pipe readers to finish, then reap the process. The readers
	// must finish before cmd.Wait because Wait closes the StdoutPipe and
	// StderrPipe read ends. A child that inherited the actor's pipes keeps
	// them open after the actor exits, but Stop/Kill send SIGTERM/SIGKILL to
	// the process group (which includes same-group children), causing them to
	// exit and close the pipes. For the rare case of a detached child (setsid),
	// Stop and Kill have their own bounded waits on p.exited.
	go func() {
		<-p.client.Done()
		<-p.stderrDone
		p.exitErr = cmd.Wait()
		close(p.exited)
	}()

	return p, nil
}

func (p *Process) captureStderr(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		p.logMu.Lock()
		_, _ = fmt.Fprintf(p.logFile, "[stderr] %s\n", line)
		p.logMu.Unlock()
	}
}

// Client returns the protocol client for this process.
func (p *Process) Client() *protocol.Client {
	return p.client
}

// Stop performs the graceful shutdown sequence:
// 1. Send scraps/shutdown, wait up to shutdownTimeout for process exit.
// 2. If still running, SIGTERM the process group, wait termTimeout.
// 3. If still running, SIGKILL the process group.
func (p *Process) Stop(ctx context.Context, shutdownTimeout, termTimeout time.Duration) error {
	// Try graceful shutdown via protocol.
	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, shutdownTimeout)
	defer shutdownCancel()

	shutdownErr := p.client.Shutdown(shutdownCtx)

	select {
	case <-p.exited:
		p.cleanup()
		return p.exitResult(shutdownErr)
	case <-shutdownCtx.Done():
	}

	// SIGTERM the process group.
	p.signalGroup(syscall.SIGTERM)

	select {
	case <-p.exited:
		p.cleanup()
		if exitErr := p.exitResult(shutdownErr); exitErr != nil {
			return fmt.Errorf("actor required SIGTERM to stop: %w", exitErr)
		}
		return errors.New("actor required SIGTERM to stop")
	case <-time.After(termTimeout):
	}

	// SIGKILL the process group.
	p.signalGroup(syscall.SIGKILL)

	select {
	case <-p.exited:
	case <-time.After(termTimeout):
		// A detached child may hold stdout open after the actor's process
		// group has been killed. Bound this wait so Stop always returns.
		_ = p.stdinPipe.Close()
		return errors.New("actor did not exit after SIGKILL (detached child may hold stdout)")
	case <-ctx.Done():
		return fmt.Errorf("actor did not exit after SIGKILL: %w", ctx.Err())
	}
	p.cleanup()
	if exitErr := p.exitResult(shutdownErr); exitErr != nil {
		return fmt.Errorf("actor required SIGKILL to stop: %w", exitErr)
	}
	return errors.New("actor required SIGKILL to stop")
}

// exitResult combines shutdown and exit errors into a single error.
func (p *Process) exitResult(shutdownErr error) error {
	if p.exitErr != nil {
		if shutdownErr != nil {
			return fmt.Errorf("exit: %w; shutdown: %w", p.exitErr, shutdownErr)
		}
		return fmt.Errorf("exit: %w", p.exitErr)
	}
	return shutdownErr
}

// ExitErr returns the error from the process exit, if any.
// It blocks until the process exits or ctx is cancelled.
func (p *Process) ExitErr(ctx context.Context) (error, bool) {
	select {
	case <-p.exited:
		return p.exitErr, true
	case <-ctx.Done():
		return ctx.Err(), false
	}
}

// Kill sends SIGTERM followed by SIGKILL after termTimeout to the process group.
// It waits up to termTimeout after SIGKILL for the process to exit.
func (p *Process) Kill(termTimeout time.Duration) {
	p.signalGroup(syscall.SIGTERM)
	select {
	case <-p.exited:
		p.cleanup()
		return
	case <-time.After(termTimeout):
	}
	p.signalGroup(syscall.SIGKILL)
	select {
	case <-p.exited:
		p.cleanup()
	case <-time.After(termTimeout):
		// Best-effort: close stdin so the process cannot read further.
		_ = p.stdinPipe.Close()
	}
}

// Exited returns a channel that is closed when the process exits.
func (p *Process) Exited() <-chan struct{} {
	return p.exited
}

// Pid returns the process ID, or -1 if not started.
func (p *Process) Pid() int {
	if p.cmd.Process == nil {
		return -1
	}
	return p.cmd.Process.Pid
}

func (p *Process) signalGroup(sig syscall.Signal) {
	if p.cmd.Process == nil {
		return
	}
	// Negative PID signals the process group.
	_ = syscall.Kill(-p.cmd.Process.Pid, sig)
}

func (p *Process) cleanup() {
	_ = p.stdinPipe.Close()
	// By the time cleanup is called, p.exited is closed, which means the
	// reaper has already waited for stderrDone. Safe to close the log file.
	<-p.stderrDone
	p.logMu.Lock()
	_ = p.logFile.Close()
	p.logMu.Unlock()
}

// CallWithTimeout sends a JSON-RPC call with a deadline.
func CallWithTimeout(ctx context.Context, client *protocol.Client, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return client.Call(callCtx, method, params)
}
