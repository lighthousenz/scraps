package actors_test

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ndjsonClient is a minimal JSON-RPC 2.0 client over stdin/stdout.
type ndjsonClient struct {
	stdin  io.WriteCloser
	stdout *bufio.Scanner
	mu     sync.Mutex
	nextID int
}

type jsonrpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (c *ndjsonClient) call(method string, params interface{}) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.nextID++
	id := c.nextID

	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	if _, err := fmt.Fprintf(c.stdin, "%s\n", data); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	// Read responses, skipping notifications
	for {
		if !c.stdout.Scan() {
			if err := c.stdout.Err(); err != nil {
				return nil, fmt.Errorf("read response: %w", err)
			}
			return nil, fmt.Errorf("stdout closed while waiting for response %d", id)
		}

		line := c.stdout.Text()
		var resp jsonrpcResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			return nil, fmt.Errorf("unmarshal response %q: %w", line, err)
		}

		// Skip notifications (no ID)
		if resp.ID == nil {
			continue
		}

		if *resp.ID != id {
			return nil, fmt.Errorf("unexpected response ID %d, expected %d", *resp.ID, id)
		}

		if resp.Error != nil {
			return nil, &rpcError{Code: resp.Error.Code, Message: resp.Error.Message}
		}

		return resp.Result, nil
	}
}

type rpcError struct {
	Code    int
	Message string
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("RPC error %d: %s", e.Code, e.Message)
}

// startActor starts an actor process using mise exec with the actor's tools.
func startActor(t *testing.T, ctx context.Context, actorDir string, tools map[string]string, run string, env []string) (*ndjsonClient, *exec.Cmd) {
	t.Helper()

	var args []string
	if len(tools) > 0 {
		args = append(args, "exec")
		// Sort tools by name for determinism
		var toolArgs []string
		for name, version := range tools {
			toolArgs = append(toolArgs, fmt.Sprintf("%s@%s", name, version))
		}
		// Simple sort
		for i := 0; i < len(toolArgs); i++ {
			for j := i + 1; j < len(toolArgs); j++ {
				if toolArgs[i] > toolArgs[j] {
					toolArgs[i], toolArgs[j] = toolArgs[j], toolArgs[i]
				}
			}
		}
		args = append(args, toolArgs...)
		args = append(args, "--", "sh", "-c", run)
	} else {
		// No tools, just run directly
		args = []string{"exec", "--", "sh", "-c", run}
	}

	cmd := exec.CommandContext(ctx, "mise", args...)
	cmd.Dir = actorDir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("start actor: %v", err)
	}

	client := &ndjsonClient{
		stdin:  stdin,
		stdout: bufio.NewScanner(stdout),
	}

	return client, cmd
}

// copyDir copies a directory tree.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}

		// Skip node_modules — it will be installed by prepare
		if strings.Contains(rel, "node_modules") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

func TestToyActor(t *testing.T) {
	if _, err := exec.LookPath("mise"); err != nil {
		t.Skip("mise not on PATH")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Copy actor to temp dir
	tmpDir := t.TempDir()
	actorDir := filepath.Join(tmpDir, "toy")
	srcDir := filepath.Join("actors", "toy")

	if err := copyDir(srcDir, actorDir); err != nil {
		t.Fatalf("copy dir: %v", err)
	}

	workdir := filepath.Join(tmpDir, "workdir")
	artefacts := filepath.Join(tmpDir, "artefacts")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir workdir: %v", err)
	}
	if err := os.MkdirAll(artefacts, 0o755); err != nil {
		t.Fatalf("mkdir artefacts: %v", err)
	}

	tools := map[string]string{"python": "3.13"}
	env := []string{
		"SCRAPS_WORKDIR=" + workdir,
		"SCRAPS_ARTEFACTS=" + artefacts,
	}

	// Test major version 1 (correct)
	t.Run("major1-correct", func(t *testing.T) {
		client, cmd := startActor(t, ctx, actorDir, tools, "python main.py", env)

		// Initialize
		result, err := client.call("scraps/initialize", map[string]interface{}{
			"protocol":  1,
			"name":      "toy",
			"kind":      "toy",
			"inputs":    map[string]interface{}{},
			"subjects":  map[string]string{"toy": "1.0.0"},
			"seed":      42,
			"workdir":   workdir,
			"artefacts": artefacts,
		})
		if err != nil {
			t.Fatalf("initialize: %v", err)
		}

		var initResult struct {
			Protocol int `json:"protocol"`
		}
		if err := json.Unmarshal(result, &initResult); err != nil {
			t.Fatalf("unmarshal init result: %v", err)
		}
		if initResult.Protocol != 1 {
			t.Errorf("protocol = %d, want 1", initResult.Protocol)
		}

		// Write
		_, err = client.call("write", map[string]interface{}{"text": "hello"})
		if err != nil {
			t.Fatalf("write: %v", err)
		}

		// Snapshot
		result, err = client.call("snapshot", map[string]interface{}{"as": "result"})
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}

		var snapResult struct {
			Files []string `json:"files"`
		}
		if err := json.Unmarshal(result, &snapResult); err != nil {
			t.Fatalf("unmarshal snap result: %v", err)
		}
		if len(snapResult.Files) != 1 || snapResult.Files[0] != "result.json" {
			t.Errorf("files = %v, want [result.json]", snapResult.Files)
		}

		// Check artefact content
		data, err := os.ReadFile(filepath.Join(artefacts, "result.json"))
		if err != nil {
			t.Fatalf("read artefact: %v", err)
		}
		var content struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(data, &content); err != nil {
			t.Fatalf("unmarshal artefact: %v", err)
		}
		if content.Text != "hello" {
			t.Errorf("text = %q, want %q", content.Text, "hello")
		}

		// Shutdown
		_, err = client.call("scraps/shutdown", map[string]interface{}{})
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}

		if err := cmd.Wait(); err != nil {
			t.Errorf("process exit: %v", err)
		}
	})

	// Test major version 2 (empties)
	t.Run("major2-empties", func(t *testing.T) {
		a2 := filepath.Join(tmpDir, "artefacts2")
		if err := os.MkdirAll(a2, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		env2 := []string{
			"SCRAPS_WORKDIR=" + workdir,
			"SCRAPS_ARTEFACTS=" + a2,
		}
		client, cmd := startActor(t, ctx, actorDir, tools, "python main.py", env2)

		_, err := client.call("scraps/initialize", map[string]interface{}{
			"protocol": 1, "name": "toy", "kind": "toy",
			"inputs":   map[string]interface{}{},
			"subjects": map[string]string{"toy": "2.0.0"},
			"seed":     42, "workdir": workdir, "artefacts": a2,
		})
		if err != nil {
			t.Fatalf("initialize: %v", err)
		}

		_, err = client.call("write", map[string]interface{}{"text": "hello"})
		if err != nil {
			t.Fatalf("write: %v", err)
		}

		_, err = client.call("snapshot", map[string]interface{}{"as": "result"})
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}

		data, err := os.ReadFile(filepath.Join(a2, "result.json"))
		if err != nil {
			t.Fatalf("read artefact: %v", err)
		}
		var content struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(data, &content); err != nil {
			t.Fatalf("unmarshal artefact: %v", err)
		}
		if content.Text != "" {
			t.Errorf("text = %q, want empty", content.Text)
		}

		_, err = client.call("scraps/shutdown", map[string]interface{}{})
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
		if err := cmd.Wait(); err != nil {
			t.Errorf("process exit: %v", err)
		}
	})

	// Test major version 3 (shouts)
	t.Run("major3-shouts", func(t *testing.T) {
		a3 := filepath.Join(tmpDir, "artefacts3")
		if err := os.MkdirAll(a3, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		env3 := []string{
			"SCRAPS_WORKDIR=" + workdir,
			"SCRAPS_ARTEFACTS=" + a3,
		}
		client, cmd := startActor(t, ctx, actorDir, tools, "python main.py", env3)

		_, err := client.call("scraps/initialize", map[string]interface{}{
			"protocol": 1, "name": "toy", "kind": "toy",
			"inputs":   map[string]interface{}{},
			"subjects": map[string]string{"toy": "3.0.0"},
			"seed":     42, "workdir": workdir, "artefacts": a3,
		})
		if err != nil {
			t.Fatalf("initialize: %v", err)
		}

		_, err = client.call("write", map[string]interface{}{"text": "hello"})
		if err != nil {
			t.Fatalf("write: %v", err)
		}

		_, err = client.call("snapshot", map[string]interface{}{"as": "result"})
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}

		data, err := os.ReadFile(filepath.Join(a3, "result.json"))
		if err != nil {
			t.Fatalf("read artefact: %v", err)
		}
		var content struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(data, &content); err != nil {
			t.Fatalf("unmarshal artefact: %v", err)
		}
		if content.Text != "HELLO" {
			t.Errorf("text = %q, want %q", content.Text, "HELLO")
		}

		_, err = client.call("scraps/shutdown", map[string]interface{}{})
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
		if err := cmd.Wait(); err != nil {
			t.Errorf("process exit: %v", err)
		}
	})

	// Test major version 4 (error 1000)
	t.Run("major4-error1000", func(t *testing.T) {
		client, cmd := startActor(t, ctx, actorDir, tools, "python main.py", env)

		_, err := client.call("scraps/initialize", map[string]interface{}{
			"protocol": 1, "name": "toy", "kind": "toy",
			"inputs":   map[string]interface{}{},
			"subjects": map[string]string{"toy": "4.0.0"},
			"seed":     42, "workdir": workdir, "artefacts": artefacts,
		})
		if err != nil {
			t.Fatalf("initialize: %v", err)
		}

		_, err = client.call("write", map[string]interface{}{"text": "hello"})
		if err == nil {
			t.Fatal("expected error from write at major 4")
		}
		var rpcErr *rpcError
		if !errors.As(err, &rpcErr) {
			t.Fatalf("expected rpcError, got %T: %v", err, err)
		}
		if rpcErr.Code != 1000 {
			t.Errorf("error code = %d, want 1000", rpcErr.Code)
		}
		if rpcErr.Message != "toy write failed" {
			t.Errorf("error message = %q, want %q", rpcErr.Message, "toy write failed")
		}

		_, err = client.call("scraps/shutdown", map[string]interface{}{})
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
		if err := cmd.Wait(); err != nil {
			t.Errorf("process exit: %v", err)
		}
	})
}

func TestParagraphsChecker(t *testing.T) {
	if _, err := exec.LookPath("mise"); err != nil {
		t.Skip("mise not on PATH")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	tmpDir := t.TempDir()
	actorDir := filepath.Join(tmpDir, "paragraphs-checker")
	srcDir := filepath.Join("actors", "paragraphs-checker")

	if err := copyDir(srcDir, actorDir); err != nil {
		t.Fatalf("copy dir: %v", err)
	}

	// Create a test artefact
	artefacts := filepath.Join(tmpDir, "artefacts")
	if err := os.MkdirAll(artefacts, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	artefactContent := `{"paragraphs": ["alpha", "beta", "gamma"]}`
	if err := os.WriteFile(filepath.Join(artefacts, "final.json"), []byte(artefactContent), 0o644); err != nil {
		t.Fatalf("write artefact: %v", err)
	}

	tools := map[string]string{"python": "3.13"}
	env := []string{
		"SCRAPS_WORKDIR=" + tmpDir,
		"SCRAPS_ARTEFACTS=" + artefacts,
	}

	client, cmd := startActor(t, ctx, actorDir, tools, "python main.py", env)

	// Initialize
	_, err := client.call("scraps/initialize", map[string]interface{}{
		"protocol": 1, "name": "paragraphs-checker", "kind": "paragraphs-checker",
		"inputs": map[string]interface{}{}, "subjects": map[string]string{},
		"seed": 42, "workdir": tmpDir, "artefacts": artefacts,
	})
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}

	artefactParam := map[string]interface{}{
		"name":  "final",
		"dir":   artefacts,
		"files": []string{"final.json"},
	}

	// has-paragraphs: match
	t.Run("has-paragraphs-match", func(t *testing.T) {
		result, err := client.call("has-paragraphs", map[string]interface{}{
			"texts":    []string{"alpha", "beta"},
			"artefact": artefactParam,
		})
		if err != nil {
			t.Fatalf("has-paragraphs: %v", err)
		}
		var r struct {
			Match bool `json:"match"`
		}
		if err := json.Unmarshal(result, &r); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !r.Match {
			t.Error("expected match")
		}
	})

	// has-paragraphs: no match
	t.Run("has-paragraphs-nomatch", func(t *testing.T) {
		result, err := client.call("has-paragraphs", map[string]interface{}{
			"texts":    []string{"alpha", "delta"},
			"artefact": artefactParam,
		})
		if err != nil {
			t.Fatalf("has-paragraphs: %v", err)
		}
		var r struct {
			Match bool `json:"match"`
		}
		if err := json.Unmarshal(result, &r); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if r.Match {
			t.Error("expected no match")
		}
	})

	// has-one-of: match
	t.Run("has-one-of-match", func(t *testing.T) {
		result, err := client.call("has-one-of", map[string]interface{}{
			"texts":    []string{"alpha", "delta"},
			"artefact": artefactParam,
		})
		if err != nil {
			t.Fatalf("has-one-of: %v", err)
		}
		var r struct {
			Match bool `json:"match"`
		}
		if err := json.Unmarshal(result, &r); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !r.Match {
			t.Error("expected match")
		}
	})

	// paragraph-count: equals
	t.Run("paragraph-count-equals", func(t *testing.T) {
		result, err := client.call("paragraph-count", map[string]interface{}{
			"equals":   3,
			"artefact": artefactParam,
		})
		if err != nil {
			t.Fatalf("paragraph-count: %v", err)
		}
		var r struct {
			Match bool `json:"match"`
		}
		if err := json.Unmarshal(result, &r); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !r.Match {
			t.Error("expected match")
		}
	})

	// Shutdown
	_, err = client.call("scraps/shutdown", map[string]interface{}{})
	if err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Errorf("process exit: %v", err)
	}
}

func TestCollabActors(t *testing.T) {
	if _, err := exec.LookPath("mise"); err != nil {
		t.Skip("mise not on PATH")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	tmpDir := t.TempDir()
	yjsVersion := "13.6.27"

	// Copy actor directories
	serverDir := filepath.Join(tmpDir, "collab-server")
	editorDir := filepath.Join(tmpDir, "editor")

	if err := copyDir(filepath.Join("actors", "collab-server"), serverDir); err != nil {
		t.Fatalf("copy collab-server: %v", err)
	}
	if err := copyDir(filepath.Join("actors", "editor"), editorDir); err != nil {
		t.Fatalf("copy editor: %v", err)
	}

	// Prepare: install dependencies
	nodeTools := map[string]string{"node": "22"}

	t.Log("preparing collab-server...")
	prepareCmd := exec.CommandContext(ctx, "mise", "exec", "node@22", "--", "sh", "-c",
		fmt.Sprintf("npm ci && npm install --no-save yjs@%s", yjsVersion))
	prepareCmd.Dir = serverDir
	prepareCmd.Stderr = os.Stderr
	prepareCmd.Stdout = os.Stderr
	if err := prepareCmd.Run(); err != nil {
		t.Fatalf("prepare collab-server: %v", err)
	}

	t.Log("preparing editor...")
	prepareCmd2 := exec.CommandContext(ctx, "mise", "exec", "node@22", "--", "sh", "-c",
		fmt.Sprintf("npm ci && npm install --no-save yjs@%s", yjsVersion))
	prepareCmd2.Dir = editorDir
	prepareCmd2.Stderr = os.Stderr
	prepareCmd2.Stdout = os.Stderr
	if err := prepareCmd2.Run(); err != nil {
		t.Fatalf("prepare editor: %v", err)
	}

	// Create fixtures
	fixtureDir := filepath.Join(tmpDir, "fixtures")
	if err := os.MkdirAll(fixtureDir, 0o755); err != nil {
		t.Fatalf("mkdir fixtures: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(fixtureDir, "paragraphs.json"),
		[]byte(`["Hello world", "Second paragraph"]`),
		0o644,
	); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	workdir := filepath.Join(tmpDir, "workdir")
	artefactsAlice := filepath.Join(tmpDir, "artefacts", "alice")
	artefactsBob := filepath.Join(tmpDir, "artefacts", "bob")

	for _, d := range []string{workdir, artefactsAlice, artefactsBob} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	serverEnv := []string{
		"SCRAPS_WORKDIR=" + workdir,
		"SCRAPS_ARTEFACTS=" + filepath.Join(tmpDir, "artefacts", "server"),
	}

	// Start the server
	t.Log("starting collab-server...")
	serverClient, serverCmd := startActor(t, ctx, serverDir, nodeTools, "node main.mjs", serverEnv)

	result, err := serverClient.call("scraps/initialize", map[string]interface{}{
		"protocol":  1,
		"name":      "server",
		"kind":      "collab-server",
		"inputs":    map[string]interface{}{"document": fixtureDir},
		"subjects":  map[string]string{"yjs": yjsVersion},
		"seed":      42,
		"workdir":   workdir,
		"artefacts": filepath.Join(tmpDir, "artefacts", "server"),
	})
	if err != nil {
		t.Fatalf("server initialize: %v", err)
	}

	var serverInit struct {
		Protocol int               `json:"protocol"`
		Outputs  map[string]string `json:"outputs"`
	}
	if err := json.Unmarshal(result, &serverInit); err != nil {
		t.Fatalf("unmarshal server init: %v", err)
	}
	if serverInit.Protocol != 1 {
		t.Errorf("server protocol = %d, want 1", serverInit.Protocol)
	}
	serverURL := serverInit.Outputs["url"]
	if !strings.HasPrefix(serverURL, "ws://127.0.0.1:") {
		t.Errorf("server url = %q, want ws://127.0.0.1:*", serverURL)
	}

	// Wait for server to be listening
	for i := 0; i < 50; i++ {
		result, err := serverClient.call("status", map[string]interface{}{})
		if err != nil {
			t.Fatalf("server status: %v", err)
		}
		var s struct {
			Listening bool `json:"listening"`
		}
		if err := json.Unmarshal(result, &s); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if s.Listening {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Start two editors
	startEditor := func(name string, artefactsDir string) (*ndjsonClient, *exec.Cmd) {
		editorEnv := []string{
			"SCRAPS_WORKDIR=" + workdir,
			"SCRAPS_ARTEFACTS=" + artefactsDir,
		}
		client, cmd := startActor(t, ctx, editorDir, nodeTools, "node main.mjs", editorEnv)

		_, err := client.call("scraps/initialize", map[string]interface{}{
			"protocol": 1,
			"name":     name,
			"kind":     "editor",
			"inputs": map[string]interface{}{
				"server": map[string]interface{}{
					"name":    "server",
					"outputs": map[string]string{"url": serverURL},
				},
			},
			"subjects":  map[string]string{"yjs": yjsVersion},
			"seed":      42,
			"workdir":   workdir,
			"artefacts": artefactsDir,
		})
		if err != nil {
			t.Fatalf("editor %s initialize: %v", name, err)
		}

		return client, cmd
	}

	t.Log("starting editors...")
	aliceClient, aliceCmd := startEditor("alice", artefactsAlice)
	bobClient, bobCmd := startEditor("bob", artefactsBob)

	// Connect both editors
	_, err = aliceClient.call("connect", map[string]interface{}{})
	if err != nil {
		t.Fatalf("alice connect: %v", err)
	}
	_, err = bobClient.call("connect", map[string]interface{}{})
	if err != nil {
		t.Fatalf("bob connect: %v", err)
	}

	// Wait for both to sync
	waitForSync := func(name string, client *ndjsonClient) {
		for i := 0; i < 100; i++ {
			result, err := client.call("status", map[string]interface{}{})
			if err != nil {
				t.Fatalf("%s status: %v", name, err)
			}
			var s struct {
				Synced bool `json:"synced"`
			}
			if err := json.Unmarshal(result, &s); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if s.Synced {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("%s did not sync in time", name)
	}

	waitForSync("alice", aliceClient)
	waitForSync("bob", bobClient)
	t.Log("both editors synced")

	// Pause sync on both
	_, err = aliceClient.call("pause-sync", map[string]interface{}{})
	if err != nil {
		t.Fatalf("alice pause-sync: %v", err)
	}
	_, err = bobClient.call("pause-sync", map[string]interface{}{})
	if err != nil {
		t.Fatalf("bob pause-sync: %v", err)
	}

	// Each inserts at start
	_, err = aliceClient.call("insert-paragraph", map[string]interface{}{"at": "start", "text": "alpha"})
	if err != nil {
		t.Fatalf("alice insert: %v", err)
	}
	_, err = bobClient.call("insert-paragraph", map[string]interface{}{"at": "start", "text": "beta"})
	if err != nil {
		t.Fatalf("bob insert: %v", err)
	}

	// Resume sync on both
	_, err = aliceClient.call("resume-sync", map[string]interface{}{})
	if err != nil {
		t.Fatalf("alice resume-sync: %v", err)
	}
	_, err = bobClient.call("resume-sync", map[string]interface{}{})
	if err != nil {
		t.Fatalf("bob resume-sync: %v", err)
	}

	// Wait for convergence: both editors should have the same digest
	t.Log("waiting for convergence...")
	for i := 0; i < 200; i++ {
		aliceResult, err := aliceClient.call("digest", map[string]interface{}{})
		if err != nil {
			t.Fatalf("alice digest: %v", err)
		}
		bobResult, err := bobClient.call("digest", map[string]interface{}{})
		if err != nil {
			t.Fatalf("bob digest: %v", err)
		}

		var ad, bd struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(aliceResult, &ad); err != nil {
			t.Fatalf("unmarshal alice digest: %v", err)
		}
		if err := json.Unmarshal(bobResult, &bd); err != nil {
			t.Fatalf("unmarshal bob digest: %v", err)
		}

		if ad.Value == bd.Value && ad.Value != "" {
			t.Logf("converged with digest %s", ad.Value)
			break
		}
		if i == 199 {
			t.Fatal("editors did not converge in time")
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Take snapshot from alice
	_, err = aliceClient.call("snapshot", map[string]interface{}{"as": "final"})
	if err != nil {
		t.Fatalf("alice snapshot: %v", err)
	}

	// Read the snapshot and check both inserts are present
	data, err := os.ReadFile(filepath.Join(artefactsAlice, "final.json"))
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	var snapshot struct {
		Paragraphs []string `json:"paragraphs"`
	}
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}

	hasAlpha := false
	hasBeta := false
	for _, p := range snapshot.Paragraphs {
		if p == "alpha" {
			hasAlpha = true
		}
		if p == "beta" {
			hasBeta = true
		}
	}
	if !hasAlpha {
		t.Error("snapshot missing 'alpha'")
	}
	if !hasBeta {
		t.Error("snapshot missing 'beta'")
	}
	t.Logf("snapshot paragraphs: %v", snapshot.Paragraphs)

	// Verify digest is the sha256 of canonical JSON
	canonical := mustJSON(snapshot.Paragraphs)
	expectedDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(canonical)))
	aliceDigest, err := aliceClient.call("digest", map[string]interface{}{})
	if err != nil {
		t.Fatalf("alice digest after snapshot: %v", err)
	}
	var digestResult struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(aliceDigest, &digestResult); err != nil {
		t.Fatalf("unmarshal digest: %v", err)
	}
	if digestResult.Value != expectedDigest {
		t.Errorf("digest = %s, want %s", digestResult.Value, expectedDigest)
	}

	// Disconnect and shutdown editors
	if _, err := aliceClient.call("disconnect", map[string]interface{}{}); err != nil {
		t.Errorf("alice disconnect: %v", err)
	}
	if _, err := bobClient.call("disconnect", map[string]interface{}{}); err != nil {
		t.Errorf("bob disconnect: %v", err)
	}

	_, err = aliceClient.call("scraps/shutdown", map[string]interface{}{})
	if err != nil {
		t.Fatalf("alice shutdown: %v", err)
	}
	_, err = bobClient.call("scraps/shutdown", map[string]interface{}{})
	if err != nil {
		t.Fatalf("bob shutdown: %v", err)
	}

	// Shutdown server
	_, err = serverClient.call("scraps/shutdown", map[string]interface{}{})
	if err != nil {
		t.Fatalf("server shutdown: %v", err)
	}

	if err := aliceCmd.Wait(); err != nil {
		t.Errorf("alice process exit: %v", err)
	}
	if err := bobCmd.Wait(); err != nil {
		t.Errorf("bob process exit: %v", err)
	}
	if err := serverCmd.Wait(); err != nil {
		t.Errorf("server process exit: %v", err)
	}
}

func mustJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
