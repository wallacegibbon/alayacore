package rawio

// Package rawio provides a raw TLV stdin/stdout adaptor for AlayaCore.
// It pipes raw bytes between stdin/stdout and the agent session —
// no parsing, no formatting, no interpretation.

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/alayacore/alayacore/internal/app"
)

// Compile-time check: Adaptor satisfies app.Adaptor.
var _ app.Adaptor = (*Adaptor)(nil)

// Adaptor pipes raw bytes between stdin/stdout and the agent session.
type Adaptor struct {
	Config *app.Config
}

// NewAdaptor creates a new rawio adaptor.
func NewAdaptor(cfg *app.Config) *Adaptor {
	return &Adaptor{Config: cfg}
}

// Start runs the rawio adaptor. It blocks until the session finishes.
// Returns 1 only on startup failure (config errors, no models).
// The controlling process reads stdout and handles TLV itself.
func (a *Adaptor) Start() int {
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

	// Wait for either EOF (stdin closed → task finishes) or SIGINT.
	select {
	case <-session.Done():
	case <-sigCh:
		inputWriter.Close()
		// Give the session a moment to finish; second SIGINT exits immediately.
		select {
		case <-session.Done():
		case <-sigCh:
		}
	}

	return 0
}
