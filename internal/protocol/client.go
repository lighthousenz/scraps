package protocol

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// Client is a JSON-RPC 2.0 client that speaks to an actor over stdin/stdout.
// It is safe for concurrent use.
type Client struct {
	stdin  io.Writer
	stdout io.Reader

	mu       sync.Mutex
	nextID   int64
	pending  map[int64]chan result
	broken   *BrokenError
	onLog    LogHandler
	closed   chan struct{}
	closeErr error

	writeMu sync.Mutex

	started atomic.Bool
}

type result struct {
	data json.RawMessage
	err  error
}

// NewClient creates a new protocol client. stdin is where requests are written;
// stdout is where responses and notifications are read from. Call Run to start
// reading from stdout.
func NewClient(stdin io.Writer, stdout io.Reader, onLog LogHandler) *Client {
	if onLog == nil {
		onLog = func(string, string) {}
	}
	return &Client{
		stdin:   stdin,
		stdout:  stdout,
		pending: make(map[int64]chan result),
		onLog:   onLog,
		closed:  make(chan struct{}),
	}
}

// Run reads from stdout and dispatches responses and notifications.
// It blocks until stdout is closed or an error occurs.
// It must be called exactly once, from a separate goroutine.
func (c *Client) Run() {
	defer close(c.closed)

	c.started.Store(true)
	scanner := bufio.NewScanner(c.stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var resp response
		if err := json.Unmarshal(line, &resp); err != nil {
			c.breakWith(fmt.Sprintf("invalid JSON on stdout: %s", err))
			return
		}

		// A notification has no id.
		if resp.ID == nil {
			if resp.JSONRPC != "2.0" || resp.Method == "" {
				c.breakWith("non-protocol JSON on stdout: missing jsonrpc or method")
				return
			}
			if resp.Method == "scraps/log" {
				var note LogNotification
				if err := json.Unmarshal(resp.Params, &note); err != nil {
					c.breakWith(fmt.Sprintf("invalid scraps/log params: %s", err))
					return
				}
				c.onLog(note.Level, note.Message)
			} else {
				c.breakWith(fmt.Sprintf("unknown notification method: %s", resp.Method))
				return
			}
			continue
		}

		c.mu.Lock()
		ch, ok := c.pending[*resp.ID]
		if !ok {
			c.mu.Unlock()
			c.breakWith(fmt.Sprintf("response to unknown id %d", *resp.ID))
			return
		}
		delete(c.pending, *resp.ID)
		c.mu.Unlock()

		if resp.Error != nil {
			mapped := mapError(resp.Error)
			var be *BrokenError
			if errors.As(mapped, &be) {
				c.breakWith(be.Reason)
			}
			ch <- result{err: mapped}
		} else {
			ch <- result{data: resp.Result}
		}
	}

	// stdout closed — latch broken and fail all pending calls.
	c.mu.Lock()
	if c.broken == nil {
		c.broken = &BrokenError{Reason: "stdout closed"}
	}
	for id, ch := range c.pending {
		ch <- result{err: c.broken}
		delete(c.pending, id)
	}
	c.closeErr = scanner.Err()
	c.mu.Unlock()
}

// Call sends a JSON-RPC request and waits for the response.
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.broken != nil {
		err := c.broken
		c.mu.Unlock()
		return nil, err
	}

	select {
	case <-c.closed:
		// Reader has exited; latch broken if not already set.
		if c.broken == nil {
			c.broken = &BrokenError{Reason: "stdout closed"}
		}
		err := c.broken
		c.mu.Unlock()
		return nil, err
	default:
	}

	c.nextID++
	id := c.nextID
	ch := make(chan result, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	var paramsBytes json.RawMessage
	if params != nil {
		var err error
		paramsBytes, err = json.Marshal(params)
		if err != nil {
			c.mu.Lock()
			delete(c.pending, id)
			c.mu.Unlock()
			return nil, fmt.Errorf("marshal params: %w", err)
		}
	}

	req := request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  paramsBytes,
	}

	data, err := json.Marshal(req)
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')

	c.writeMu.Lock()
	_, err = c.stdin.Write(data)
	c.writeMu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("write request: %w", err)
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		c.breakWith(fmt.Sprintf("call to %s timed out", method))
		return nil, c.broken
	case r := <-ch:
		return r.data, r.err
	case <-c.closed:
		c.mu.Lock()
		// Check if result arrived before closed.
		select {
		case r := <-ch:
			c.mu.Unlock()
			return r.data, r.err
		default:
		}
		delete(c.pending, id)
		if c.broken == nil {
			c.broken = &BrokenError{Reason: "stdout closed"}
		}
		err := c.broken
		c.mu.Unlock()
		return nil, err
	}
}

// Initialize sends scraps/initialize and validates the protocol version.
func (c *Client) Initialize(ctx context.Context, params *InitializeParams) (*InitializeResult, error) {
	if params.Protocol != ProtocolVersion {
		return nil, fmt.Errorf("unsupported protocol version %d (this client supports %d)", params.Protocol, ProtocolVersion)
	}

	// Normalize nil maps to empty maps so actors receive {} not null.
	if params.Inputs == nil {
		params.Inputs = make(map[string]any)
	}
	if params.Subjects == nil {
		params.Subjects = make(map[string]string)
	}

	raw, err := c.Call(ctx, "scraps/initialize", params)
	if err != nil {
		return nil, fmt.Errorf("scraps/initialize: %w", err)
	}

	var res InitializeResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("unmarshal initialize result: %w", err)
	}

	if res.Protocol != params.Protocol {
		c.breakWith(fmt.Sprintf("protocol version mismatch: sent %d, got %d", params.Protocol, res.Protocol))
		return nil, c.broken
	}

	return &res, nil
}

// Shutdown sends scraps/shutdown.
func (c *Client) Shutdown(ctx context.Context) error {
	_, err := c.Call(ctx, "scraps/shutdown", struct{}{})
	return err
}

// IsBroken returns the broken error if the client is broken, or nil.
func (c *Client) IsBroken() *BrokenError {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.broken
}

// Done returns a channel that is closed when the reader goroutine exits.
func (c *Client) Done() <-chan struct{} {
	return c.closed
}

func (c *Client) breakWith(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.broken != nil {
		return
	}
	c.broken = &BrokenError{Reason: reason}
	for id, ch := range c.pending {
		ch <- result{err: c.broken}
		delete(c.pending, id)
	}
}

func mapError(e *responseError) error {
	switch e.Code {
	case CodeOperationFailed:
		return &OperationFailedError{
			Message: e.Message,
			Data:    e.Data,
		}
	default:
		// 1001, -32601, -32602, and any other code -> broken.
		return &BrokenError{Reason: fmt.Sprintf("error code %d: %s", e.Code, e.Message)}
	}
}
