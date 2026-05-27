package plainio

// Package plainio provides a plain stdin/stdout adaptor for AlayaCore.
// It reads prompts from stdin (one per line) and prints messages to stdout.
// No terminal features are used — just plain IO.

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/stream"
)

// Compile-time check: Adaptor satisfies app.Adaptor.
var _ app.Adaptor = (*Adaptor)(nil)

// Adaptor reads prompts from stdin and prints assistant output to stdout.
type Adaptor struct {
	Config *app.Config
}

// NewAdaptor creates a new plainio adaptor.
func NewAdaptor(cfg *app.Config) *Adaptor {
	return &Adaptor{Config: cfg}
}

// Start runs the plainio adaptor. It blocks until the session finishes.
// Returns the exit code: 0 for graceful exit, 1 for errors, 130 (128+SIGINT)
// for Ctrl-C.
//
// plainio processes prompts one at a time. If a task produces an error
// (SE tag), the remaining input is discarded and the process exits
// with code 1 — queued tasks are NOT executed.
func (a *Adaptor) Start() int {
	input := stream.NewSliceReadWriter(100)
	output := newStdoutOutput()

	// Load session
	session, err := app.StartSession(a.Config, input, output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT)
	defer signal.Stop(sigCh)

	exitCh := make(chan int, 1)

	// Read stdin and emit TLV messages.
	go func() {
		if err := readPromptsFromStdin(input); err != nil {
			select {
			case exitCh <- 1:
			default:
			}
			return
		}
		input.Close()
		select {
		case exitCh <- 0:
		default:
		}
	}()

	// Main goroutine owns all signal handling. No SIGINT goroutine.
	// First exit trigger wins: EOF (0), Ctrl-C (130), or error via
	// the output.ErrorChannel() path (1).
	code := 0
	select {
	case code = <-exitCh:
	case <-output.ErrorChannel():
		code = 1
		input.Close()
	case <-sigCh:
		code = 128 + int(syscall.SIGINT)
		input.Close()
	}

	// Let the current task finish, but a second Ctrl-C forces immediate exit.
	select {
	case <-session.Done():
	case <-sigCh:
		code = 128 + int(syscall.SIGINT)
	}

	// Final check: even on a clean EOF path the session may have written
	// errors (network failures, API errors, etc.) that arrived after the
	// stdin goroutine closed input.  Override the exit code.
	if code == 0 && output.HasError() {
		code = 1
	}

	return code
}
