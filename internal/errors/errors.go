// Package errors provides domain-specific error types for AlayaCore.
// These errors provide structured context about what operation failed.
package errors

import "fmt"

// ============================================================================
// Domain Errors
// ============================================================================

// Model errors
var (
	// ErrModelNotFound indicates the requested model was not found
	ErrModelNotFound = &SessionError{Op: "model_set", Err: fmt.Errorf("model not found")}

	// ErrModelManagerNotInitialized indicates the model manager is not initialized
	ErrModelManagerNotInitialized = &SessionError{Op: "model", Err: fmt.Errorf("model manager not initialized")}

	// ErrNoModelFilePath indicates no model file path is configured
	ErrNoModelFilePath = &SessionError{Op: "model_load", Err: fmt.Errorf("no model file path configured")}

	// ErrFailedToLoadModels indicates models could not be loaded
	ErrFailedToLoadModels = &SessionError{Op: "model_load", Err: fmt.Errorf("failed to load models")}
)

// Queue errors
var (
	// ErrQueueItemNotFound indicates the queue item was not found
	ErrQueueItemNotFound = &SessionError{Op: "taskqueue_del", Err: fmt.Errorf("queue item not found")}
)

// Session errors
var (
	// ErrNoSessionFile indicates no session file is set
	ErrNoSessionFile = &SessionError{Op: "save", Err: fmt.Errorf("no session file set")}

	// ErrFailedToSaveSession indicates the session could not be saved
	ErrFailedToSaveSession = &SessionError{Op: "save", Err: fmt.Errorf("failed to save session")}
)

// Command errors
var (
	// ErrEmptyCommand indicates an empty command was received
	ErrEmptyCommand = &SessionError{Op: "command", Err: fmt.Errorf("empty command")}

	// ErrNothingToCancel indicates there is nothing to cancel
	ErrNothingToCancel = &SessionError{Op: "cancel", Err: fmt.Errorf("nothing to cancel")}
)

// Input errors
var (
	// ErrInvalidInputTag indicates an invalid TLV tag was received
	ErrInvalidInputTag = &SessionError{Op: "input", Err: fmt.Errorf("invalid input tag")}
)

// ============================================================================
// Structured Error Types
// ============================================================================

// SessionError represents an error with operation context.
// It provides structured information about what operation failed.
type SessionError struct {
	Op  string // The operation that failed (e.g., "model_set", "save")
	Err error  // The underlying error
}

// Error implements the error interface.
func (e *SessionError) Error() string {
	if e.Err == nil {
		return e.Op
	}
	return e.Err.Error()
}

// Unwrap returns the underlying error for use with errors.Is and errors.As.
func (e *SessionError) Unwrap() error {
	return e.Err
}

// Operation returns the operation that failed.
func (e *SessionError) Operation() string {
	return e.Op
}

// NewSessionError creates a new SessionError with the given operation and error.
func NewSessionError(op string, err error) *SessionError {
	return &SessionError{Op: op, Err: err}
}

// NewSessionErrorf creates a new SessionError with a formatted error message.
func NewSessionErrorf(op, format string, args ...any) *SessionError {
	return &SessionError{Op: op, Err: fmt.Errorf(format, args...)}
}

// Wrap wraps an existing error with operation context.
func Wrap(op string, err error) *SessionError {
	return &SessionError{Op: op, Err: err}
}

// Wrapf wraps an error with operation context and a formatted message.
func Wrapf(op string, err error, format string, args ...any) *SessionError {
	return &SessionError{Op: op, Err: fmt.Errorf(format+": %w", append(args, err)...)}
}
