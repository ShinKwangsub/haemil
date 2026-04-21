package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// defaultCallTimeout caps any single request/response round trip.
const defaultCallTimeout = 60 * time.Second

// stdioClient is a low-level JSON-RPC client over a subprocess's stdio.
//
// Thread-safety: Call/Notify are safe for concurrent use. Internally a
// single background goroutine reads from stdout and dispatches to pending
// Call waiters keyed by request id. Requests are serialised by a write
// mutex so two concurrent writers don't interleave frames on stdin.
type stdioClient struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	writeMu sync.Mutex

	nextID atomic.Int64

	pendingMu sync.Mutex
	pending   map[int64]chan *Response

	doneCh chan struct{}
	readErr error
	closed  atomic.Bool
}

// newStdioClient spawns `command args...` with env, wires stdin/stdout
// pipes, and starts the read loop. The subprocess's stderr is forwarded to
// the parent process's stderr for visibility.
func newStdioClient(ctx context.Context, command string, args []string, env []string) (*stdioClient, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	if len(env) > 0 {
		cmd.Env = env
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdout pipe: %w", err)
	}
	// Inherit stderr so server errors surface to the user.
	cmd.Stderr = nil // defaults to /dev/null unless set; we want visibility
	// Instead use the parent's stderr directly.
	cmd.Stderr = stderrOf()

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: start %q: %w", command, err)
	}

	c := &stdioClient{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		pending: make(map[int64]chan *Response),
		doneCh:  make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

// Call issues a request and waits for the matching response, honouring
// both ctx and timeout. Returns the raw result bytes, or the RPC error
// wrapped as a Go error.
func (c *stdioClient) Call(ctx context.Context, method string, params interface{}, timeout time.Duration) (json.RawMessage, error) {
	if c.closed.Load() {
		return nil, errors.New("mcp: client closed")
	}
	if timeout <= 0 {
		timeout = defaultCallTimeout
	}
	id := c.nextID.Add(1)

	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("mcp: marshal params: %w", err)
		}
		rawParams = b
	}
	req := Request{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Method:  method,
		Params:  rawParams,
	}

	ch := make(chan *Response, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	if err := c.writeFrame(req); err != nil {
		return nil, err
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case <-callCtx.Done():
		return nil, fmt.Errorf("mcp: call %q: %w", method, callCtx.Err())
	case <-c.doneCh:
		if c.readErr != nil {
			return nil, fmt.Errorf("mcp: client stopped: %w", c.readErr)
		}
		return nil, errors.New("mcp: client stopped")
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp: call %q: %w", method, resp.Error)
		}
		return resp.Result, nil
	}
}

// Notify sends a fire-and-forget notification.
func (c *stdioClient) Notify(method string, params interface{}) error {
	if c.closed.Load() {
		return errors.New("mcp: client closed")
	}
	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("mcp: marshal params: %w", err)
		}
		rawParams = b
	}
	note := Notification{
		JSONRPC: jsonRPCVersion,
		Method:  method,
		Params:  rawParams,
	}
	return c.writeFrame(note)
}

// writeFrame marshals v and writes `<json>\n` to stdin.
func (c *stdioClient) writeFrame(v interface{}) error {
	buf, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("mcp: marshal frame: %w", err)
	}
	buf = append(buf, '\n')

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.stdin.Write(buf); err != nil {
		return fmt.Errorf("mcp: write frame: %w", err)
	}
	return nil
}

// readLoop consumes newline-delimited JSON from stdout and dispatches
// responses to the pending waiter keyed by id. Server-initiated requests
// (e.g. roots/list, sampling/createMessage) are not supported in MVP —
// we log and ignore them. The loop exits on stdout EOF or decode error.
func (c *stdioClient) readLoop() {
	defer close(c.doneCh)
	scanner := bufio.NewScanner(c.stdout)
	// Allow large frames — tool results can carry big payloads.
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		// Try to decode as a Response first (has "id" + result|error).
		// If it has "method" and no "id", it's a notification from server.
		var probe struct {
			ID     *int64           `json:"id"`
			Method string           `json:"method"`
			Result json.RawMessage  `json:"result,omitempty"`
			Error  json.RawMessage  `json:"error,omitempty"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			// Unparseable line — keep going (some servers log to stdout
			// despite the protocol forbidding it).
			continue
		}
		if probe.ID != nil && probe.Method == "" {
			var resp Response
			if err := json.Unmarshal(line, &resp); err != nil {
				continue
			}
			c.pendingMu.Lock()
			ch, ok := c.pending[resp.ID]
			c.pendingMu.Unlock()
			if ok {
				ch <- &resp
			}
			continue
		}
		// Server-initiated request or notification — not yet supported.
		// Silently drop.
	}
	if err := scanner.Err(); err != nil {
		c.readErr = err
	}
}

// Close shuts the subprocess down: closes stdin (signaling graceful exit),
// waits briefly, then kills if still running.
func (c *stdioClient) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	_ = c.stdin.Close()
	// Give the server a chance to exit cleanly.
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = c.cmd.Process.Kill()
		<-done
	}
	return nil
}
