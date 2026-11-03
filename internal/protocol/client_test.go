package protocol_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lighthousenz/scraps/internal/protocol"
)

// fakeActor simulates an actor's stdin/stdout for testing.
type fakeActor struct {
	// requests written by the client appear here
	reqR *io.PipeReader
	reqW *io.PipeWriter

	// responses written by the test appear here
	respR *io.PipeReader
	respW *io.PipeWriter
}

func newFakeActor() *fakeActor {
	reqR, reqW := io.Pipe()
	respR, respW := io.Pipe()
	return &fakeActor{reqR: reqR, reqW: reqW, respR: respR, respW: respW}
}

func (f *fakeActor) close() {
	_ = f.reqW.Close()
	_ = f.reqR.Close()
	_ = f.respW.Close()
	_ = f.respR.Close()
}

// sendResponse writes a JSON-RPC response line to the actor's stdout.
func (f *fakeActor) sendResponse(t *testing.T, id int64, result any) {
	t.Helper()
	res, _ := json.Marshal(result)
	line := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, id, res)
	_, err := fmt.Fprintln(f.respW, line)
	if err != nil {
		t.Errorf("sendResponse: %v", err)
	}
}

// sendError writes a JSON-RPC error response.
func (f *fakeActor) sendError(t *testing.T, id int64, code int, message string, data any) {
	t.Helper()
	errObj := map[string]any{"code": code, "message": message}
	if data != nil {
		d, _ := json.Marshal(data)
		errObj["data"] = json.RawMessage(d)
	}
	e, _ := json.Marshal(errObj)
	line := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"error":%s}`, id, e)
	_, err := fmt.Fprintln(f.respW, line)
	if err != nil {
		t.Errorf("sendError: %v", err)
	}
}

// sendNotification writes a JSON-RPC notification.
func (f *fakeActor) sendNotification(t *testing.T, method string, params any) {
	t.Helper()
	p, _ := json.Marshal(params)
	line := fmt.Sprintf(`{"jsonrpc":"2.0","method":%q,"params":%s}`, method, p)
	_, err := fmt.Fprintln(f.respW, line)
	if err != nil {
		t.Errorf("sendNotification: %v", err)
	}
}

// readRequest reads and returns the next request from the client.
func (f *fakeActor) readRequest(t *testing.T) (id int64, method string, params json.RawMessage) {
	t.Helper()
	buf := make([]byte, 0, 4096)
	b := make([]byte, 1)
	for {
		n, err := f.reqR.Read(b)
		if err != nil {
			t.Fatalf("readRequest: %v", err)
		}
		if n > 0 && b[0] == '\n' {
			break
		}
		buf = append(buf, b[:n]...)
	}

	var req struct {
		ID     int64           `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(buf, &req); err != nil {
		t.Fatalf("readRequest unmarshal: %v", err)
	}
	return req.ID, req.Method, req.Params
}

func TestCallAndResponse(t *testing.T) {
	fa := newFakeActor()
	defer fa.close()

	var logs []string
	c := protocol.NewClient(fa.reqW, fa.respR, func(level, message string) {
		logs = append(logs, level+": "+message)
	})
	go c.Run()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	var result json.RawMessage
	var callErr error
	go func() {
		defer wg.Done()
		result, callErr = c.Call(ctx, "test/method", map[string]string{"key": "value"})
	}()

	id, method, _ := fa.readRequest(t)
	if method != "test/method" {
		t.Errorf("expected method test/method, got %s", method)
	}
	fa.sendResponse(t, id, map[string]string{"answer": "42"})

	wg.Wait()
	if callErr != nil {
		t.Fatalf("unexpected error: %v", callErr)
	}

	var res map[string]string
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if res["answer"] != "42" {
		t.Errorf("expected answer=42, got %v", res)
	}
}

func TestResponsesOutOfOrder(t *testing.T) {
	fa := newFakeActor()
	defer fa.close()

	c := protocol.NewClient(fa.reqW, fa.respR, nil)
	go c.Run()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Send two concurrent calls.
	var wg sync.WaitGroup
	var result1, result2 json.RawMessage
	var err1, err2 error

	wg.Add(2)
	go func() {
		defer wg.Done()
		result1, err1 = c.Call(ctx, "method/first", nil)
	}()
	go func() {
		defer wg.Done()
		result2, err2 = c.Call(ctx, "method/second", nil)
	}()

	// Read both requests.
	id1, _, _ := fa.readRequest(t)
	id2, _, _ := fa.readRequest(t)

	// Respond in reverse order.
	fa.sendResponse(t, id2, map[string]string{"from": "second"})
	fa.sendResponse(t, id1, map[string]string{"from": "first"})

	wg.Wait()

	if err1 != nil {
		t.Fatalf("call 1 error: %v", err1)
	}
	if err2 != nil {
		t.Fatalf("call 2 error: %v", err2)
	}

	var r1, r2 map[string]string
	if err := json.Unmarshal(result1, &r1); err != nil {
		t.Fatalf("unmarshal result1: %v", err)
	}
	if err := json.Unmarshal(result2, &r2); err != nil {
		t.Fatalf("unmarshal result2: %v", err)
	}

	if r1["from"] != "first" {
		t.Errorf("result1 expected from=first, got %v", r1)
	}
	if r2["from"] != "second" {
		t.Errorf("result2 expected from=second, got %v", r2)
	}
}

func TestNotifications(t *testing.T) {
	fa := newFakeActor()
	defer fa.close()

	var mu sync.Mutex
	var logs []string
	c := protocol.NewClient(fa.reqW, fa.respR, func(level, message string) {
		mu.Lock()
		defer mu.Unlock()
		logs = append(logs, level+": "+message)
	})
	go c.Run()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = c.Call(ctx, "test/op", nil)
	}()

	id, _, _ := fa.readRequest(t)

	fa.sendNotification(t, "scraps/log", protocol.LogNotification{Level: "info", Message: "hello"})
	fa.sendNotification(t, "scraps/log", protocol.LogNotification{Level: "warn", Message: "caution"})

	// Small sleep to allow notification processing.
	time.Sleep(50 * time.Millisecond)

	fa.sendResponse(t, id, struct{}{})
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(logs) != 2 {
		t.Fatalf("expected 2 log notifications, got %d: %v", len(logs), logs)
	}
	if logs[0] != "info: hello" {
		t.Errorf("unexpected log[0]: %s", logs[0])
	}
	if logs[1] != "warn: caution" {
		t.Errorf("unexpected log[1]: %s", logs[1])
	}
}

func TestErrorCode1000_OperationFailed(t *testing.T) {
	fa := newFakeActor()
	defer fa.close()

	c := protocol.NewClient(fa.reqW, fa.respR, nil)
	go c.Run()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	var callErr error
	go func() {
		defer wg.Done()
		_, callErr = c.Call(ctx, "test/op", nil)
	}()

	id, _, _ := fa.readRequest(t)
	fa.sendError(t, id, 1000, "toy write failed", map[string]string{"detail": "bad"})
	wg.Wait()

	var opErr *protocol.OperationFailedError
	if !errors.As(callErr, &opErr) {
		t.Fatalf("expected OperationFailedError, got %T: %v", callErr, callErr)
	}
	if opErr.Message != "toy write failed" {
		t.Errorf("expected message 'toy write failed', got %q", opErr.Message)
	}

	// Client should NOT be broken after code 1000.
	if c.IsBroken() != nil {
		t.Error("client should not be broken after code 1000")
	}
}

func TestErrorCode1001_Broken(t *testing.T) {
	fa := newFakeActor()
	defer fa.close()

	c := protocol.NewClient(fa.reqW, fa.respR, nil)
	go c.Run()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	var callErr error
	go func() {
		defer wg.Done()
		_, callErr = c.Call(ctx, "test/op", nil)
	}()

	id, _, _ := fa.readRequest(t)
	fa.sendError(t, id, 1001, "actor fault", nil)
	wg.Wait()

	var brokenErr *protocol.BrokenError
	if !errors.As(callErr, &brokenErr) {
		t.Fatalf("expected BrokenError, got %T: %v", callErr, callErr)
	}
}

func TestErrorCodeMethodNotFound_Broken(t *testing.T) {
	fa := newFakeActor()
	defer fa.close()

	c := protocol.NewClient(fa.reqW, fa.respR, nil)
	go c.Run()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	var callErr error
	go func() {
		defer wg.Done()
		_, callErr = c.Call(ctx, "test/op", nil)
	}()

	id, _, _ := fa.readRequest(t)
	fa.sendError(t, id, -32601, "Method not found", nil)
	wg.Wait()

	var brokenErr *protocol.BrokenError
	if !errors.As(callErr, &brokenErr) {
		t.Fatalf("expected BrokenError, got %T: %v", callErr, callErr)
	}
}

func TestErrorCodeInvalidParams_Broken(t *testing.T) {
	fa := newFakeActor()
	defer fa.close()

	c := protocol.NewClient(fa.reqW, fa.respR, nil)
	go c.Run()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	var callErr error
	go func() {
		defer wg.Done()
		_, callErr = c.Call(ctx, "test/op", nil)
	}()

	id, _, _ := fa.readRequest(t)
	fa.sendError(t, id, -32602, "Invalid params", nil)
	wg.Wait()

	var brokenErr *protocol.BrokenError
	if !errors.As(callErr, &brokenErr) {
		t.Fatalf("expected BrokenError, got %T: %v", callErr, callErr)
	}
}

func TestProtocolViolation_InvalidJSON(t *testing.T) {
	fa := newFakeActor()
	defer fa.close()

	c := protocol.NewClient(fa.reqW, fa.respR, nil)
	go c.Run()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	var callErr error
	go func() {
		defer wg.Done()
		_, callErr = c.Call(ctx, "test/op", nil)
	}()

	fa.readRequest(t)
	if _, err := fmt.Fprintln(fa.respW, "this is not JSON"); err != nil {
		t.Fatalf("write invalid JSON: %v", err)
	}

	wg.Wait()

	var brokenErr *protocol.BrokenError
	if !errors.As(callErr, &brokenErr) {
		t.Fatalf("expected BrokenError, got %T: %v", callErr, callErr)
	}
	if !strings.Contains(brokenErr.Reason, "invalid JSON") {
		t.Errorf("expected 'invalid JSON' in reason, got %q", brokenErr.Reason)
	}
}

func TestProtocolViolation_UnknownID(t *testing.T) {
	fa := newFakeActor()
	defer fa.close()

	c := protocol.NewClient(fa.reqW, fa.respR, nil)
	go c.Run()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	var callErr error
	go func() {
		defer wg.Done()
		_, callErr = c.Call(ctx, "test/op", nil)
	}()

	fa.readRequest(t)
	// Respond with a bogus ID.
	fa.sendResponse(t, 99999, struct{}{})

	wg.Wait()

	var brokenErr *protocol.BrokenError
	if !errors.As(callErr, &brokenErr) {
		t.Fatalf("expected BrokenError, got %T: %v", callErr, callErr)
	}
	if !strings.Contains(brokenErr.Reason, "unknown id") {
		t.Errorf("expected 'unknown id' in reason, got %q", brokenErr.Reason)
	}
}

func TestProtocolViolation_StdoutClosedWithPending(t *testing.T) {
	fa := newFakeActor()

	c := protocol.NewClient(fa.reqW, fa.respR, nil)
	go c.Run()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	var callErr error
	go func() {
		defer wg.Done()
		_, callErr = c.Call(ctx, "test/op", nil)
	}()

	fa.readRequest(t)
	// Close stdout while call is pending.
	_ = fa.respW.Close()

	wg.Wait()

	var brokenErr *protocol.BrokenError
	if !errors.As(callErr, &brokenErr) {
		t.Fatalf("expected BrokenError, got %T: %v", callErr, callErr)
	}
	if !strings.Contains(brokenErr.Reason, "stdout closed") {
		t.Errorf("expected 'stdout closed' in reason, got %q", brokenErr.Reason)
	}
	_ = fa.reqR.Close()
	_ = fa.reqW.Close()
}

func TestVersionMismatch(t *testing.T) {
	fa := newFakeActor()
	defer fa.close()

	c := protocol.NewClient(fa.reqW, fa.respR, nil)
	go c.Run()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	var callErr error
	go func() {
		defer wg.Done()
		_, callErr = c.Initialize(ctx, &protocol.InitializeParams{
			Protocol: 1,
			Name:     "test",
			Kind:     "test",
		})
	}()

	id, _, _ := fa.readRequest(t)
	fa.sendResponse(t, id, protocol.InitializeResult{Protocol: 2, Outputs: nil})
	wg.Wait()

	var brokenErr *protocol.BrokenError
	if !errors.As(callErr, &brokenErr) {
		t.Fatalf("expected BrokenError, got %T: %v", callErr, callErr)
	}
	if !strings.Contains(brokenErr.Reason, "protocol version mismatch") {
		t.Errorf("expected 'protocol version mismatch' in reason, got %q", brokenErr.Reason)
	}
}

func TestShutdownClean(t *testing.T) {
	fa := newFakeActor()
	defer fa.close()

	c := protocol.NewClient(fa.reqW, fa.respR, nil)
	go c.Run()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	var shutdownErr error
	go func() {
		defer wg.Done()
		shutdownErr = c.Shutdown(ctx)
	}()

	id, method, _ := fa.readRequest(t)
	if method != "scraps/shutdown" {
		t.Errorf("expected scraps/shutdown, got %s", method)
	}
	fa.sendResponse(t, id, struct{}{})
	wg.Wait()

	if shutdownErr != nil {
		t.Errorf("unexpected shutdown error: %v", shutdownErr)
	}
}

func TestContextCancellation(t *testing.T) {
	fa := newFakeActor()
	defer fa.close()

	c := protocol.NewClient(fa.reqW, fa.respR, nil)
	go c.Run()

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)
	var callErr error
	go func() {
		defer wg.Done()
		_, callErr = c.Call(ctx, "test/op", nil)
	}()

	fa.readRequest(t)
	// Cancel the context without responding.
	cancel()

	wg.Wait()

	var brokenErr *protocol.BrokenError
	if !errors.As(callErr, &brokenErr) {
		t.Fatalf("expected BrokenError, got %T: %v", callErr, callErr)
	}
}

func TestBrokenStatePersists(t *testing.T) {
	fa := newFakeActor()
	defer fa.close()

	c := protocol.NewClient(fa.reqW, fa.respR, nil)
	go c.Run()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Trigger broken by sending invalid JSON.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = c.Call(ctx, "test/first", nil)
	}()

	fa.readRequest(t)
	_, _ = fmt.Fprintln(fa.respW, "not json")
	wg.Wait()

	// Subsequent calls should fail immediately.
	_, err := c.Call(ctx, "test/second", nil)
	var brokenErr *protocol.BrokenError
	if !errors.As(err, &brokenErr) {
		t.Fatalf("expected BrokenError on subsequent call, got %T: %v", err, err)
	}
}

func TestInitializeSuccess(t *testing.T) {
	fa := newFakeActor()
	defer fa.close()

	c := protocol.NewClient(fa.reqW, fa.respR, nil)
	go c.Run()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	var initResult *protocol.InitializeResult
	var initErr error
	go func() {
		defer wg.Done()
		initResult, initErr = c.Initialize(ctx, &protocol.InitializeParams{
			Protocol:  1,
			Name:      "alice",
			Kind:      "editor",
			Inputs:    map[string]any{"server": "server"},
			Subjects:  map[string]string{"yjs": "13.6.27"},
			Seed:      12345,
			Workdir:   "/tmp/workdir",
			Artefacts: "/tmp/artefacts",
		})
	}()

	id, method, params := fa.readRequest(t)
	if method != "scraps/initialize" {
		t.Errorf("expected scraps/initialize, got %s", method)
	}
	var p protocol.InitializeParams
	if err := json.Unmarshal(params, &p); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if p.Protocol != 1 {
		t.Errorf("expected protocol 1, got %d", p.Protocol)
	}
	if p.Name != "alice" {
		t.Errorf("expected name alice, got %s", p.Name)
	}

	fa.sendResponse(t, id, protocol.InitializeResult{
		Protocol: 1,
		Outputs:  map[string]any{"client-id": "alice-1"},
	})
	wg.Wait()

	if initErr != nil {
		t.Fatalf("unexpected error: %v", initErr)
	}
	if initResult.Protocol != 1 {
		t.Errorf("expected protocol 1, got %d", initResult.Protocol)
	}
	clientID, ok := initResult.Outputs["client-id"]
	if !ok || clientID != "alice-1" {
		t.Errorf("expected client-id=alice-1, got %v", initResult.Outputs)
	}
}
