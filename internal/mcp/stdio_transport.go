package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/alayacore/alayacore/internal/debug"
)

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
	pending   map[requestID]chan<- jsonrpcResponse
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
		pending: make(map[requestID]chan<- jsonrpcResponse),
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
// Server-to-client requests (e.g. ping) are handled inline.
func (t *StdioTransport) readLoop() {
	defer t.readerWg.Done()

	for t.scanner.Scan() {
		data := t.scanner.Bytes()
		if err := parseAndDispatchJSONRPC(data, t.pending, &t.pendingMu, t.debugWriter, t.handleServerRequest); err != nil {
			// Malformed line — log and skip (server bug or protocol mismatch).
			log.Printf("MCP: malformed response line (len=%d): %v",
				len(data), err)
		}
	}

	// Scanner error or EOF — close all remaining pending channels.
	t.pendingMu.Lock()
	for id, ch := range t.pending {
		close(ch)
		delete(t.pending, id)
	}
	t.pendingMu.Unlock()
}

// handleServerRequest handles a JSON-RPC request from the server (e.g. ping).
// Responses are sent back through the transport.
func (t *StdioTransport) handleServerRequest(id requestID, method string) {
	switch method {
	case methodPing:
		// Respond with empty result.
		resp := jsonrpcResponse{
			JSONRPC: jsonrpcVersion,
			ID:      id,
			Result:  json.RawMessage(`{}`),
		}
		data, _ := json.Marshal(resp) //nolint:errcheck // static struct, cannot fail
		t.mu.Lock()
		t.stdin.Write(append(data, '\n')) //nolint:errcheck // best-effort
		t.mu.Unlock()

	default:
		// Method not found — respond with error.
		resp := jsonrpcResponse{
			JSONRPC: jsonrpcVersion,
			ID:      id,
			Error: &jsonrpcError{
				Code:    -32601, // METHOD_NOT_FOUND
				Message: "Method not found: " + method,
			},
		}
		data, _ := json.Marshal(resp) //nolint:errcheck // static struct, cannot fail
		t.mu.Lock()
		t.stdin.Write(append(data, '\n')) //nolint:errcheck // best-effort
		t.mu.Unlock()
	}
}
func (t *StdioTransport) Send(ctx context.Context, req jsonrpcRequest) error {
	_ = ctx
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

	if err := t.Send(ctx, req); err != nil {
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

// Close terminates the MCP server process gracefully per the MCP spec:
//  1. Close stdin to signal EOF to the server
//  2. Wait for the server to exit (with timeout)
//  3. SIGTERM if still running
//  4. SIGKILL if still running after another timeout
func (t *StdioTransport) Close() error {
	t.closeOnce.Do(func() {
		// Step 1: Close stdin to signal EOF.
		t.stdin.Close()

		// Step 2: Wait for the process to exit on its own.
		done := make(chan struct{})
		go func() {
			t.cmd.Wait() //nolint:errcheck // exit status captured in done signal
			close(done)
		}()

		select {
		case <-done:
			// Process exited cleanly.
		case <-time.After(2 * time.Second):
			// Step 3: SIGTERM.
			if t.cmd != nil && t.cmd.Process != nil {
				t.cmd.Process.Signal(os.Signal(syscall.SIGTERM)) //nolint:errcheck // best-effort
			}
			select {
			case <-done:
				// Process exited after SIGTERM.
			case <-time.After(3 * time.Second):
				// Step 4: SIGKILL.
				if t.cmd != nil && t.cmd.Process != nil {
					t.cmd.Process.Kill() //nolint:errcheck // SIGKILL always succeeds on Unix
				}
				<-done
			}
		}

		// Wait for readLoop to finish processing pending responses.
		t.readerWg.Wait()

		// Signal that transport is done.
		close(t.done)
	})
	return nil
}

// Done returns a channel that closes when the process exits.
func (t *StdioTransport) Done() <-chan struct{} {
	return t.done
}
