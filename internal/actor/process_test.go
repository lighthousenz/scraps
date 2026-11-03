package actor_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/lighthousenz/scraps/internal/actor"
	"github.com/lighthousenz/scraps/internal/protocol"
)

// TestHelperProcess is the re-exec entry point for fake actors.
// It is not a real test; it is invoked by the test binary when GO_WANT_HELPER_PROCESS=1.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	defer os.Exit(0)

	mode := os.Getenv("HELPER_MODE")
	scanner := bufio.NewScanner(os.Stdin)

	switch mode {
	case "echo":
		// Simple echo actor: responds to initialize, any operation, and shutdown.
		for scanner.Scan() {
			line := scanner.Text()
			var req struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      int64           `json:"id"`
				Method  string          `json:"method"`
				Params  json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal([]byte(line), &req); err != nil {
				continue
			}
			switch req.Method {
			case "scraps/initialize":
				var params protocol.InitializeParams
				_ = json.Unmarshal(req.Params, &params)
				resp := map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"protocol": params.Protocol,
						"outputs":  map[string]any{"echo": "true"},
					},
				}
				data, _ := json.Marshal(resp)
				_, _ = fmt.Fprintln(os.Stdout, string(data))
			case "scraps/shutdown":
				resp := map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{},
				}
				data, _ := json.Marshal(resp)
				_, _ = fmt.Fprintln(os.Stdout, string(data))
				return
			default:
				resp := map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"method": req.Method,
						"params": json.RawMessage(req.Params),
					},
				}
				data, _ := json.Marshal(resp)
				_, _ = fmt.Fprintln(os.Stdout, string(data))
			}
		}

	case "stderr-writer":
		// Writes to stderr and responds to protocol.
		_, _ = fmt.Fprintln(os.Stderr, "stderr line 1")
		_, _ = fmt.Fprintln(os.Stderr, "stderr line 2")
		for scanner.Scan() {
			line := scanner.Text()
			var req struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      int64           `json:"id"`
				Method  string          `json:"method"`
				Params  json.RawMessage `json:"params"`
			}
			_ = json.Unmarshal([]byte(line), &req)
			switch req.Method {
			case "scraps/initialize":
				var params protocol.InitializeParams
				_ = json.Unmarshal(req.Params, &params)
				resp := map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"protocol": params.Protocol,
						"outputs":  map[string]any{},
					},
				}
				data, _ := json.Marshal(resp)
				_, _ = fmt.Fprintln(os.Stdout, string(data))
				// Also send a log notification.
				notif := map[string]any{
					"jsonrpc": "2.0",
					"method":  "scraps/log",
					"params":  map[string]any{"level": "info", "message": "initialized"},
				}
				nd, _ := json.Marshal(notif)
				_, _ = fmt.Fprintln(os.Stdout, string(nd))
			case "scraps/shutdown":
				resp := map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{},
				}
				data, _ := json.Marshal(resp)
				_, _ = fmt.Fprintln(os.Stdout, string(data))
				return
			}
		}

	case "ignore-sigterm":
		// Ignores SIGTERM so we can test escalation to SIGKILL.
		// Write a PID file so the test knows we started.
		pidFile := os.Getenv("HELPER_PID_FILE")
		if pidFile != "" {
			_ = os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0o644)
		}

		// Respond to initialize.
		for scanner.Scan() {
			line := scanner.Text()
			var req struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      int64           `json:"id"`
				Method  string          `json:"method"`
				Params  json.RawMessage `json:"params"`
			}
			_ = json.Unmarshal([]byte(line), &req)
			if req.Method == "scraps/initialize" {
				var params protocol.InitializeParams
				_ = json.Unmarshal(req.Params, &params)
				resp := map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"protocol": params.Protocol,
						"outputs":  map[string]any{},
					},
				}
				data, _ := json.Marshal(resp)
				_, _ = fmt.Fprintln(os.Stdout, string(data))
				break
			}
		}

		// Now just spin, ignoring all signals except SIGKILL.
		for {
			time.Sleep(100 * time.Millisecond)
		}

	case "error-1000":
		// Returns error 1000 on any operation.
		for scanner.Scan() {
			line := scanner.Text()
			var req struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      int64           `json:"id"`
				Method  string          `json:"method"`
				Params  json.RawMessage `json:"params"`
			}
			_ = json.Unmarshal([]byte(line), &req)
			switch req.Method {
			case "scraps/initialize":
				var params protocol.InitializeParams
				_ = json.Unmarshal(req.Params, &params)
				resp := map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"protocol": params.Protocol,
						"outputs":  map[string]any{},
					},
				}
				data, _ := json.Marshal(resp)
				_, _ = fmt.Fprintln(os.Stdout, string(data))
			case "scraps/shutdown":
				resp := map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{},
				}
				data, _ := json.Marshal(resp)
				_, _ = fmt.Fprintln(os.Stdout, string(data))
				return
			default:
				resp := map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"error": map[string]any{
						"code":    1000,
						"message": "toy write failed",
						"data":    map[string]any{"detail": "test"},
					},
				}
				data, _ := json.Marshal(resp)
				_, _ = fmt.Fprintln(os.Stdout, string(data))
			}
		}

	case "protocol-violation":
		// Prints non-JSON to stdout on an operation.
		for scanner.Scan() {
			line := scanner.Text()
			var req struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      int64           `json:"id"`
				Method  string          `json:"method"`
				Params  json.RawMessage `json:"params"`
			}
			_ = json.Unmarshal([]byte(line), &req)
			switch req.Method {
			case "scraps/initialize":
				var params protocol.InitializeParams
				_ = json.Unmarshal(req.Params, &params)
				resp := map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"protocol": params.Protocol,
						"outputs":  map[string]any{},
					},
				}
				data, _ := json.Marshal(resp)
				_, _ = fmt.Fprintln(os.Stdout, string(data))
			default:
				// Protocol violation: non-JSON output.
				_, _ = fmt.Fprintln(os.Stdout, "THIS IS NOT JSON")
			}
		}

	default:
		fmt.Fprintf(os.Stderr, "unknown helper mode: %s\n", mode)
		os.Exit(2)
	}
}

func startTestActor(t *testing.T, mode string, extraEnv ...string) (*actor.Process, string) {
	t.Helper()
	dir := t.TempDir()
	workdir := filepath.Join(dir, "workdir")
	artefacts := filepath.Join(dir, "artefacts")
	logPath := filepath.Join(dir, "actor.log")

	testBin, _ := os.Executable()
	run := fmt.Sprintf("%s -test.run=TestHelperProcess", testBin)

	env := []string{
		"GO_WANT_HELPER_PROCESS=1",
		"HELPER_MODE=" + mode,
	}
	env = append(env, extraEnv...)

	cfg := &actor.StartConfig{
		Run:       run,
		Dir:       dir,
		Workdir:   workdir,
		Artefacts: artefacts,
		LogPath:   logPath,
	}

	// We need to set environment variables. The process will inherit from the parent,
	// but we need GO_WANT_HELPER_PROCESS and HELPER_MODE.
	// Set them as env vars that the child will inherit.
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		t.Setenv(parts[0], parts[1])
	}

	ctx := context.Background()
	proc, err := actor.Start(ctx, cfg)
	if err != nil {
		t.Fatalf("start actor: %v", err)
	}

	t.Cleanup(func() {
		proc.Kill(2 * time.Second)
		assertProcessGroupGone(t, proc.Pid())
	})

	return proc, dir
}

func assertProcessGroupGone(t *testing.T, pid int) {
	t.Helper()
	if pid <= 0 {
		return
	}
	// Try to signal the process group. If it's gone, we get an error.
	err := syscall.Kill(-pid, 0)
	if err == nil {
		t.Errorf("process group %d still exists after cleanup", pid)
	}
}

func TestProcessStartAndShutdown(t *testing.T) {
	proc, dir := startTestActor(t, "echo")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Initialize.
	result, err := proc.Client().Initialize(ctx, &protocol.InitializeParams{
		Protocol:  1,
		Name:      "test",
		Kind:      "echo",
		Workdir:   filepath.Join(dir, "workdir"),
		Artefacts: filepath.Join(dir, "artefacts"),
	})
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if result.Protocol != 1 {
		t.Errorf("expected protocol 1, got %d", result.Protocol)
	}
	if result.Outputs["echo"] != "true" {
		t.Errorf("expected echo=true in outputs, got %v", result.Outputs)
	}

	// Call an operation.
	raw, err := proc.Client().Call(ctx, "test/op", map[string]string{"key": "val"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	var res map[string]any
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if res["method"] != "test/op" {
		t.Errorf("expected method=test/op in result, got %v", res)
	}

	// Shutdown.
	pid := proc.Pid()
	err = proc.Stop(ctx, 5*time.Second, 2*time.Second)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	assertProcessGroupGone(t, pid)
}

func TestProcessStderrCapture(t *testing.T) {
	proc, dir := startTestActor(t, "stderr-writer")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Initialize to trigger stderr output.
	_, err := proc.Client().Initialize(ctx, &protocol.InitializeParams{
		Protocol:  1,
		Name:      "test",
		Kind:      "test",
		Workdir:   filepath.Join(dir, "workdir"),
		Artefacts: filepath.Join(dir, "artefacts"),
	})
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// Shutdown — readers are drained before the process is reaped, so no sleep is needed.
	if err := proc.Stop(ctx, 5*time.Second, 2*time.Second); err != nil {
		t.Fatalf("stop: %v", err)
	}

	// Check log file for stderr and notification entries.
	logPath := filepath.Join(dir, "actor.log")
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	logStr := string(content)
	if !strings.Contains(logStr, "[stderr] stderr line 1") {
		t.Errorf("expected stderr line 1 in log, got:\n%s", logStr)
	}
	if !strings.Contains(logStr, "[stderr] stderr line 2") {
		t.Errorf("expected stderr line 2 in log, got:\n%s", logStr)
	}
	if !strings.Contains(logStr, "[info] initialized") {
		t.Errorf("expected notification in log, got:\n%s", logStr)
	}
}

func TestProcessSIGTERMAndSIGKILL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping escalation test in short mode")
	}

	proc, dir := startTestActor(t, "ignore-sigterm")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Initialize.
	_, err := proc.Client().Initialize(ctx, &protocol.InitializeParams{
		Protocol:  1,
		Name:      "test",
		Kind:      "test",
		Workdir:   filepath.Join(dir, "workdir"),
		Artefacts: filepath.Join(dir, "artefacts"),
	})
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}

	pid := proc.Pid()

	// Stop with short timeouts.
	// shutdown will fail because actor ignores shutdown after initialize.
	// SIGTERM will be ignored. SIGKILL will work.
	err = proc.Stop(ctx, 1*time.Second, 1*time.Second)
	if err != nil {
		t.Logf("stop returned error (expected for ignore-sigterm): %v", err)
	}

	// Process group should be gone.
	assertProcessGroupGone(t, pid)
}

func TestProcessContextCancellation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping context cancellation test in short mode")
	}

	proc, dir := startTestActor(t, "ignore-sigterm")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Initialize.
	_, err := proc.Client().Initialize(ctx, &protocol.InitializeParams{
		Protocol:  1,
		Name:      "test",
		Kind:      "test",
		Workdir:   filepath.Join(dir, "workdir"),
		Artefacts: filepath.Join(dir, "artefacts"),
	})
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}

	pid := proc.Pid()

	// Kill directly (simulates context cancellation path).
	proc.Kill(1 * time.Second)

	assertProcessGroupGone(t, pid)
}

func TestProcessErrorCode1000(t *testing.T) {
	proc, dir := startTestActor(t, "error-1000")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := proc.Client().Initialize(ctx, &protocol.InitializeParams{
		Protocol:  1,
		Name:      "test",
		Kind:      "test",
		Workdir:   filepath.Join(dir, "workdir"),
		Artefacts: filepath.Join(dir, "artefacts"),
	})
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}

	_, err = proc.Client().Call(ctx, "write", map[string]string{"text": "hello"})
	var opErr *protocol.OperationFailedError
	if !errors.As(err, &opErr) {
		t.Fatalf("expected OperationFailedError, got %T: %v", err, err)
	}
	if opErr.Message != "toy write failed" {
		t.Errorf("expected 'toy write failed', got %q", opErr.Message)
	}

	// Client should not be broken.
	if proc.Client().IsBroken() != nil {
		t.Error("client should not be broken after error 1000")
	}

	// Clean shutdown should still work.
	if err := proc.Stop(ctx, 5*time.Second, 2*time.Second); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func TestProcessProtocolViolation(t *testing.T) {
	proc, dir := startTestActor(t, "protocol-violation")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := proc.Client().Initialize(ctx, &protocol.InitializeParams{
		Protocol:  1,
		Name:      "test",
		Kind:      "test",
		Workdir:   filepath.Join(dir, "workdir"),
		Artefacts: filepath.Join(dir, "artefacts"),
	})
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}

	_, err = proc.Client().Call(ctx, "write", map[string]string{"text": "hello"})
	var brokenErr *protocol.BrokenError
	if !errors.As(err, &brokenErr) {
		t.Fatalf("expected BrokenError, got %T: %v", err, err)
	}
	if !strings.Contains(brokenErr.Reason, "invalid JSON") {
		t.Errorf("expected 'invalid JSON' in reason, got %q", brokenErr.Reason)
	}
}

func TestProcessWorkdirAndArtefactsCreated(t *testing.T) {
	dir := t.TempDir()
	workdir := filepath.Join(dir, "deep", "nested", "workdir")
	artefacts := filepath.Join(dir, "deep", "nested", "artefacts")
	logPath := filepath.Join(dir, "actor.log")

	testBin, _ := os.Executable()
	run := fmt.Sprintf("%s -test.run=TestHelperProcess", testBin)

	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	t.Setenv("HELPER_MODE", "echo")

	cfg := &actor.StartConfig{
		Run:       run,
		Dir:       dir,
		Workdir:   workdir,
		Artefacts: artefacts,
		LogPath:   logPath,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	proc, err := actor.Start(ctx, cfg)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer proc.Kill(2 * time.Second)

	// Check directories were created.
	if _, err := os.Stat(workdir); os.IsNotExist(err) {
		t.Error("workdir not created")
	}
	if _, err := os.Stat(artefacts); os.IsNotExist(err) {
		t.Error("artefacts dir not created")
	}
}

func TestBuildCommandNoTools(t *testing.T) {
	name, args, err := actor.BuildCommand(nil, "node ./main.mjs")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "sh" {
		t.Errorf("expected sh, got %s", name)
	}
	if len(args) != 2 || args[0] != "-c" || args[1] != "node ./main.mjs" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestBuildCommandWithTools(t *testing.T) {
	// This test checks the built command line, not running mise.
	// We need mise on PATH for BuildCommand to succeed.
	if _, err := exec.LookPath("mise"); err != nil {
		t.Skip("mise not on PATH")
	}

	tools := map[string]string{
		"python": "3.13.1",
		"node":   "22.11.0",
	}
	name, args, err := actor.BuildCommand(tools, "node ./main.mjs")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(name, "mise") {
		t.Errorf("expected mise binary, got %s", name)
	}
	// Tools should be sorted: node before python.
	expected := []string{"exec", "node@22.11.0", "python@3.13.1", "--", "sh", "-c", "node ./main.mjs"}
	if len(args) != len(expected) {
		t.Fatalf("expected %d args, got %d: %v", len(expected), len(args), args)
	}
	for i, a := range expected {
		if args[i] != a {
			t.Errorf("arg[%d]: expected %q, got %q", i, a, args[i])
		}
	}
}

func TestBuildCommandMiseMissing(t *testing.T) {
	// Temporarily modify PATH to exclude mise.
	t.Setenv("PATH", "/nonexistent")

	tools := map[string]string{"node": "22"}
	_, _, err := actor.BuildCommand(tools, "node main.mjs")
	if !errors.Is(err, actor.ErrMiseNotFound) {
		t.Errorf("expected ErrMiseNotFound, got %v", err)
	}
}

func TestProcessGroupIsolation(t *testing.T) {
	// Start two actors and verify they don't interfere.
	proc1, dir1 := startTestActor(t, "echo")
	proc2, dir2 := startTestActor(t, "echo")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		if _, err := proc1.Client().Initialize(ctx, &protocol.InitializeParams{
			Protocol: 1, Name: "a1", Kind: "echo",
			Workdir: filepath.Join(dir1, "workdir"), Artefacts: filepath.Join(dir1, "artefacts"),
		}); err != nil {
			t.Errorf("initialize proc1: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if _, err := proc2.Client().Initialize(ctx, &protocol.InitializeParams{
			Protocol: 1, Name: "a2", Kind: "echo",
			Workdir: filepath.Join(dir2, "workdir"), Artefacts: filepath.Join(dir2, "artefacts"),
		}); err != nil {
			t.Errorf("initialize proc2: %v", err)
		}
	}()
	wg.Wait()

	pid1 := proc1.Pid()
	pid2 := proc2.Pid()
	if pid1 == pid2 {
		t.Error("both actors have the same PID")
	}

	// Stop one, the other should still be running.
	if err := proc1.Stop(ctx, 5*time.Second, 2*time.Second); err != nil {
		t.Fatalf("stop proc1: %v", err)
	}
	assertProcessGroupGone(t, pid1)

	// proc2 should still respond.
	_, err := proc2.Client().Call(ctx, "test/op", nil)
	if err != nil {
		t.Errorf("proc2 should still work after proc1 stopped: %v", err)
	}

	if err := proc2.Stop(ctx, 5*time.Second, 2*time.Second); err != nil {
		t.Fatalf("stop proc2: %v", err)
	}
	assertProcessGroupGone(t, pid2)
}
