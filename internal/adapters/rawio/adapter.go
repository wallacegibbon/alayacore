package rawio

// Package rawio provides a raw TLV stdin/stdout adapter for AlayaCore.
// It pipes raw bytes between stdin/stdout and the agent session -
// no parsing, no formatting, no interpretation.

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/alayacore/alayacore/internal/app"
)

// Compile-time check: Adapter satisfies app.Adapter.
var _ app.Adapter = (*Adapter)(nil)

// Adapter pipes raw bytes between stdin/stdout and the agent session.
type Adapter struct {
	Config *app.Config
}

// NewAdapter creates a new rawio adapter.
func NewAdapter(cfg *app.Config) *Adapter {
	return &Adapter{Config: cfg}
}

// Start runs the rawio adapter. It blocks until the session finishes.
// Returns 0 on success, 1 on any error (startup or task failure).
// The controlling process reads stdout and handles TLV itself.
func (a *Adapter) Start() int {
	session, inputWriter, err := app.StartSession(a.Config, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT)
	defer signal.Stop(sigCh)

	// Pipe stdin to the session.
	go func() {
		_, _ = io.Copy(inputWriter, os.Stdin) //nolint:errcheck // stdin EOF is normal termination
		inputWriter.Close()
	}()

	// Wait for either EOF (stdin closed -> task finishes) or SIGINT.
	// Closing os.Stdin signals the copy goroutine to stop; it will close
	// inputWriter itself, so there is never a double-close race.
	select {
	case <-session.Done():
	case <-sigCh:
		os.Stdin.Close()
		// Give the session a moment to finish; second SIGINT exits immediately.
		select {
		case <-session.Done():
		case <-sigCh:
		}
	}

	if session.TaskError() {
		return 1
	}

	return 0
}
