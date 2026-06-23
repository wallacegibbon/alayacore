package rawio

// Package rawio provides a raw TLV stdin/stdout adapter for AlayaCore.
// It pipes raw bytes between stdin/stdout and the agent session -
// no parsing, no formatting, no interpretation.

import (
	"fmt"
	"io"
	"os"

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
// Ctrl-C (SIGINT) terminates immediately with default signal handling.
func (a *Adapter) Start() int {
	session, inputWriter, err := app.StartSession(a.Config, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	// Pipe stdin to the session.
	go func() {
		_, _ = io.Copy(inputWriter, os.Stdin) //nolint:errcheck // stdin EOF is normal termination
		inputWriter.Close()
	}()

	// Wait for the session to finish.
	<-session.Done()

	return 0
}
