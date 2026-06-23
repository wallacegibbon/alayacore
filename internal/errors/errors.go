// Package errors provides domain-specific error types for AlayaCore.
// These errors provide structured context about what operation failed.
package errors

import "fmt"

// ============================================================================
// Sentinel Errors
//
// Standard Go sentinels created with errors.New(). Operation context is added
// by Wrap(), Wrapf(), or NewSessionErrorf() at the call site.
// Use errors.Is(err, ErrFoo) to check for a specific sentinel.
// ============================================================================

// Model errors
var (
	ErrModelNotFound              = fmt.Errorf("model not found")
	ErrModelManagerNotInitialized = fmt.Errorf("model manager not initialized")
	ErrNoModelFilePath            = fmt.Errorf("no model file path configured")
	ErrFailedToLoadModels         = fmt.Errorf("failed to load models")
)

// Queue errors
var (
	ErrQueueItemNotFound = fmt.Errorf("queue item not found")
)

// Session errors
var (
	ErrNoSessionFile       = fmt.Errorf("no session file set")
	ErrFailedToSaveSession = fmt.Errorf("failed to save session")
)

// Command errors
var (
	ErrEmptyCommand    = fmt.Errorf("empty command")
	ErrNothingToCancel = fmt.Errorf("nothing to cancel")
)

// Input errors
var (
	ErrInvalidInputTag = fmt.Errorf("invalid input tag")
)

// Provider errors
var (
	ErrProviderCreationFailed = fmt.Errorf("provider creation failed")
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
