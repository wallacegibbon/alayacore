// Package errors provides domain-specific error types for AlayaCore.
// These errors provide structured context about what operation failed.
package errors

import "fmt"

// ============================================================================
// Sentinel Errors (message templates without operation context)
//
// These carry an empty Op. Operation context is added by Wrap(), Wrapf(),
// or NewSessionErrorf() at the call site. This way Error() never produces
// duplicated "op: op: message" strings when sentinels are wrapped.
// ============================================================================

// Model errors
var (
	ErrModelNotFound              = &SessionError{Err: fmt.Errorf("model not found")}
	ErrModelManagerNotInitialized = &SessionError{Err: fmt.Errorf("model manager not initialized")}
	ErrNoModelFilePath            = &SessionError{Err: fmt.Errorf("no model file path configured")}
	ErrFailedToLoadModels         = &SessionError{Err: fmt.Errorf("failed to load models")}
)

// Queue errors
var (
	ErrQueueItemNotFound = &SessionError{Err: fmt.Errorf("queue item not found")}
)

// Session errors
var (
	ErrNoSessionFile       = &SessionError{Err: fmt.Errorf("no session file set")}
	ErrFailedToSaveSession = &SessionError{Err: fmt.Errorf("failed to save session")}
)

// Command errors
var (
	ErrEmptyCommand    = &SessionError{Err: fmt.Errorf("empty command")}
	ErrNothingToCancel = &SessionError{Err: fmt.Errorf("nothing to cancel")}
)

// Input errors
var (
	ErrInvalidInputTag = &SessionError{Err: fmt.Errorf("invalid input tag")}
)

// Provider errors
var (
	ErrProviderCreationFailed = &SessionError{Err: fmt.Errorf("provider creation failed")}
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
// Includes the operation context (Op) when set, producing "<op>: <message>".
func (e *SessionError) Error() string {
	if e.Err == nil {
		return e.Op
	}
	if e.Op != "" {
		return e.Op + ": " + e.Err.Error()
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

// NewSessionErrorf creates a new SessionError with a formatted error message.
func NewSessionErrorf(op, format string, args ...any) *SessionError {
	return &SessionError{Op: op, Err: fmt.Errorf(format, args...)}
}

// Wrap wraps an existing error with operation context.
func Wrap(op string, err error) *SessionError {
	if err == nil {
		return nil
	}
	return &SessionError{Op: op, Err: err}
}

// Wrapf wraps an error with operation context and a formatted message.
func Wrapf(op string, err error, format string, args ...any) *SessionError {
	if err == nil {
		return nil
	}
	return &SessionError{Op: op, Err: fmt.Errorf(format+": %w", append(args, err)...)}
}
