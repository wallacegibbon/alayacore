package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"

	"github.com/alayacore/alayacore/internal/debug"
)

// Transport defines the interface for MCP communication channels.
// MCP uses JSON-RPC 2.0 over either stdio or SSE.
type Transport interface {
	// Send marshals and sends a JSON-RPC request (no response expected).
	Send(req jsonrpcRequest) error

	// SendReceive sends a JSON-RPC request and waits for the matching
	// response, matched by request ID. Context cancellation unregisters
	// the pending request without affecting the transport — the response
	// is simply discarded when it arrives.
	SendReceive(ctx context.Context, req jsonrpcRequest) (json.RawMessage, error)

	// Close shuts down the transport.
	Close() error

	// Done returns a channel that's closed when the transport has
	// encountered a fatal error or been closed.
	Done() <-chan struct{}
}

// ============================================================================
// Stdio Transport
// ============================================================================

// StdioTransport communicates with an MCP server via stdin/stdout.
// JSON-RPC messages are newline-delimited JSON (NDJSON).
//
// A single background goroutine (readLoop) reads all response lines from
// the scanner and dispatches them to waiting callers by request ID.
// This eliminates per-call goroutine leaks and prevents response
// desynchronization on context cancellation.
type StdioTransport struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	scanner   *bufio.Scanner
	closeOnce sync.Once
	done      chan struct{}
	mu        sync.Mutex // protects send serialization

	// readLoop dispatches responses here by request ID.
	pending   map[int]chan<- jsonrpcResponse
	pendingMu sync.Mutex
	readerWg  sync.WaitGroup

	debugWriter io.Writer // non-nil when --debug-mcp is enabled; logs raw JSON-RPC
}

// NewStdioTransport creates a stdio transport that spawns the given command.
func NewStdioTransport(command string, args []string, env map[string]string, enableDebug bool) (*StdioTransport, error) {
	cmd := exec.Command(command, args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}

	// MCP servers typically log to stderr; forward it to our stderr.
	cmd.Stderr = nil // Let server's stderr flow to our stderr by default

	// Set environment variables.
	if len(env) > 0 {
		cmd.Env = append(cmd.Environ(), mapToEnvSlice(env)...)
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("start command: %w", err)
	}

	t := &StdioTransport{
		cmd:     cmd,
		stdin:   stdin,
		scanner: bufio.NewScanner(stdout),
		done:    make(chan struct{}),
		pending: make(map[int]chan<- jsonrpcResponse),
	}

	// Initialize debug writer if requested.
	if enableDebug {
		t.debugWriter = debug.NewDebugWriter("alayacore-debug-mcp")
		if t.debugWriter != nil {
			fmt.Fprintf(t.debugWriter, "MCP debug log started for: %s %v\n", command, args)
		}
	}

	// Start dedicated reader goroutine.
	t.readerWg.Add(1)
	go t.readLoop()

	// Monitor for process exit.
	go func() {
		cmd.Wait() //nolint:errcheck // process exit detected via close(t.done) by closeOnce
		t.closeOnce.Do(func() { close(t.done) })
	}()

	return t, nil
}

// readLoop is the dedicated background goroutine that reads all JSON-RPC
// response lines from the scanner and dispatches them by request ID.
func (t *StdioTransport) readLoop() {
	defer t.readerWg.Done()

	for t.scanner.Scan() {
		var resp jsonrpcResponse
		if err := json.Unmarshal(t.scanner.Bytes(), &resp); err != nil {
			// Malformed line — log and skip (server bug or protocol mismatch).
			log.Printf("MCP: malformed response line (len=%d): %v",
				len(t.scanner.Bytes()), err)
			continue
		}

		t.pendingMu.Lock()
		ch, ok := t.pending[resp.ID]
		if ok {
			delete(t.pending, resp.ID)
		}
		t.pendingMu.Unlock()

		if t.debugWriter != nil {
			fmt.Fprintf(t.debugWriter, "<<< %s\n", formatJSON(t.scanner.Bytes()))
		}

		if ok {
			// Non-blocking send; channel has buffer 1 and receiver is
			// waiting (unless context was canceled after lookup).
			select {
			case ch <- resp:
			default:
			}
			close(ch)
		}
		// No pending request for this ID — discard the response.
	}

	// Scanner error or EOF — close all remaining pending channels.
	t.pendingMu.Lock()
	for id, ch := range t.pending {
		close(ch)
		delete(t.pending, id)
	}
	t.pendingMu.Unlock()
}

// Send writes a JSON-RPC request as a newline-terminated JSON message.
func (t *StdioTransport) Send(req jsonrpcRequest) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	data = append(data, '\n')

	if t.debugWriter != nil {
		fmt.Fprintf(t.debugWriter, ">>> %s %s\n", req.Method, formatJSON(data[:len(data)-1]))
	}

	if _, err := t.stdin.Write(data); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	return nil
}

// SendReceive sends a JSON-RPC request and waits for the matching response
// by request ID. On context cancellation, the pending request is unregistered
// and the response is discarded when it arrives — no transport disruption.
func (t *StdioTransport) SendReceive(ctx context.Context, req jsonrpcRequest) (json.RawMessage, error) {
	// Create a buffered channel for this request's response.
	respCh := make(chan jsonrpcResponse, 1)

	// Register the pending request before sending, so there's no race
	// between a fast response arriving and registration.
	t.pendingMu.Lock()
	select {
	case <-t.done:
		t.pendingMu.Unlock()
		return nil, io.EOF
	default:
	}
	t.pending[req.ID] = respCh
	t.pendingMu.Unlock()

	// Double-check: done may have been closed between the first check and
	// registration (readLoop exit → monitor closing done).  If so,
	// clean up immediately — don't leave an orphaned pending entry.
	select {
	case <-t.done:
		t.pendingMu.Lock()
		delete(t.pending, req.ID)
		t.pendingMu.Unlock()
		return nil, io.EOF
	default:
	}

	// Remove from pending map on any exit path.
	var success bool
	defer func() {
		if !success {
			t.pendingMu.Lock()
			delete(t.pending, req.ID)
			t.pendingMu.Unlock()
		}
	}()

	if err := t.Send(req); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}

	// Wait for the matching response.
	select {
	case resp, ok := <-respCh:
		if !ok {
			return nil, io.EOF
		}
		if resp.Error != nil {
			return nil, &RPCError{
				Code:    resp.Error.Code,
				Message: resp.Error.Message,
				Data:    resp.Error.Data,
			}
		}
		success = true
		return resp.Result, nil

	case <-ctx.Done():
		// Context canceled — unregister (defer handles cleanup).
		// The response will arrive and be discarded by readLoop.
		return nil, ctx.Err()

	case <-t.done:
		return nil, io.EOF
	}
}

// Close terminates the MCP server process.
// Order: kill process (closes pipes, unblocks scanner) → wait for reader →
// close stdin → signal done.
func (t *StdioTransport) Close() error {
	t.closeOnce.Do(func() {
		// Kill the process first — this closes stdout pipe, which
		// unblocks the scanner and lets readLoop exit.
		if t.cmd != nil && t.cmd.Process != nil {
			t.cmd.Process.Kill() //nolint:errcheck // SIGKILL always succeeds on Unix, best-effort on Windows
		}

		// Wait for readLoop to finish processing pending responses.
		t.readerWg.Wait()

		// Close stdin.
		t.stdin.Close()

		// Signal that transport is done.
		close(t.done)
	})
	return nil
}

// Done returns a channel that closes when the process exits.
func (t *StdioTransport) Done() <-chan struct{} {
	return t.done
}

// ============================================================================
// SSE Transport (placeholder — will be implemented fully later)
// ============================================================================

// SSETransport communicates with an MCP server via Server-Sent Events.
// JSON-RPC requests are sent as HTTP POST, responses arrive as SSE events.
type SSETransport struct {
	url       string
	closeOnce sync.Once
	done      chan struct{}
}

// NewSSETransport creates a new SSE transport.
// This is a minimal placeholder — full SSE support will be added as needed.
func NewSSETransport(url string) *SSETransport {
	return &SSETransport{
		url:  url,
		done: make(chan struct{}),
	}
}

// Send sends a JSON-RPC request via HTTP POST.
func (t *SSETransport) Send(_ jsonrpcRequest) error {
	// TODO: Implement SSE transport
	return fmt.Errorf("mcp sse: not yet implemented")
}

// SendReceive sends and receives via SSE.
func (t *SSETransport) SendReceive(_ context.Context, _ jsonrpcRequest) (json.RawMessage, error) {
	// TODO: Implement SSE transport
	return nil, fmt.Errorf("mcp sse: not yet implemented")
}

// Close shuts down the SSE connection.
func (t *SSETransport) Close() error {
	t.closeOnce.Do(func() {
		close(t.done)
	})
	return nil
}

// Done returns a channel that closes when the connection is done.
func (t *SSETransport) Done() <-chan struct{} {
	return t.done
}

// ============================================================================
// Helpers
// ============================================================================

// mapToEnvSlice converts a map[string]string to "KEY=VALUE" strings.
func mapToEnvSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	s := make([]string, 0, len(env))
	for k, v := range env {
		s = append(s, k+"="+v)
	}
	return s
}

// formatJSON pretty-prints a JSON byte slice for debug logging.
// Falls back to the raw string on error.
func formatJSON(data []byte) string {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return string(data)
	}
	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return string(data)
	}
	return string(pretty)
}
