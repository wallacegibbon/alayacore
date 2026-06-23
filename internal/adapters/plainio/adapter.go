package plainio

// Package plainio provides a plain stdin/stdout adapter for AlayaCore.
// It reads prompts from stdin (one per line) and prints messages to stdout.
// No terminal features are used - just plain IO.

import (
	"fmt"
	"os"

	"github.com/alayacore/alayacore/internal/app"
)

// Compile-time check: Adapter satisfies app.Adapter.
var _ app.Adapter = (*Adapter)(nil)

// Adapter reads prompts from stdin and prints assistant output to stdout.
type Adapter struct {
	Config *app.Config
}

// NewAdapter creates a new plainio adapter.
func NewAdapter(cfg *app.Config) *Adapter {
	return &Adapter{Config: cfg}
}

// Start runs the plainio adapter. It blocks until the session finishes.
// Returns 0 on success, 1 on errors. Ctrl-C (SIGINT) terminates immediately
// with default signal handling (exit code 130).
//
// plainio processes prompts one at a time. If a task produces an error
// (TagSystemMsg with type "error"), the remaining input is discarded and the process exits
// with code 1 - errors are reported immediately.
func (a *Adapter) Start() int {
	output := newStdoutOutput()

	// Load session
	session, inputWriter, err := app.StartSession(a.Config, output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	exitCh := make(chan int, 1)

	// Read stdin and emit TLV messages. On read error, close inputWriter
	// to signal EOF. Only the stdin goroutine touches inputWriter.
	go func() {
		err := readPrompts(inputWriter, os.Stdin)
		// Close signals EOF regardless, unblocking the session.
		inputWriter.Close()
		if err != nil {
			select {
			case exitCh <- 1:
			default:
			}
			return
		}
		select {
		case exitCh <- 0:
		default:
		}
	}()

	// Wait for EOF (Ctrl-D), error, or session completion.
	code := 0
	select {
	case code = <-exitCh:
	case <-output.ErrorChannel():
		code = 1
		os.Stdin.Close()
	case <-session.Done():
	}

	// Wait for the session to finish processing.
	<-session.Done()

	// Final check: even on a clean EOF path the session may have written
	// errors (network failures, API errors, etc.) that arrived after the
	// stdin goroutine closed input.  Override the exit code.
	if code == 0 && output.HasError() {
		code = 1
	}

	return code
}
