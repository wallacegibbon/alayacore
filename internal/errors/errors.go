// Package errors provides domain-specific error types for AlayaCore.
// These errors provide structured context about what operation failed.
package errors

import "fmt"

// ============================================================================
// Sentinel Errors (SessionError with default operation context)
//
// These carry a default Op so they work standalone. Use Wrap() or
// NewSessionError() to override the operation at the call site.
// ============================================================================

// Model errors
var (
	ErrModelNotFound              = &SessionError{Op: "model_set", Err: fmt.Errorf("model not found")}
	ErrModelManagerNotInitialized = &SessionError{Op: "model", Err: fmt.Errorf("model manager not initialized")}
	ErrNoModelFilePath            = &SessionError{Op: "model_load", Err: fmt.Errorf("no model file path configured")}
	ErrFailedToLoadModels         = &SessionError{Op: "model_load", Err: fmt.Errorf("failed to load models")}
	ErrModelConfigInvalid         = &SessionError{Op: "model", Err: fmt.Errorf("invalid model configuration")}
)

// Queue errors
var (
	ErrQueueItemNotFound = &SessionError{Op: "taskqueue_del", Err: fmt.Errorf("queue item not found")}
)

// Session errors
var (
	ErrNoSessionFile       = &SessionError{Op: "save", Err: fmt.Errorf("no session file set")}
	ErrFailedToSaveSession = &SessionError{Op: "save", Err: fmt.Errorf("failed to save session")}
)

// Command errors
var (
	ErrEmptyCommand    = &SessionError{Op: "command", Err: fmt.Errorf("empty command")}
	ErrNothingToCancel = &SessionError{Op: "cancel", Err: fmt.Errorf("nothing to cancel")}
)

// Input errors
var (
	ErrInvalidInputTag = &SessionError{Op: "input", Err: fmt.Errorf("invalid input tag")}
)

// Provider errors
var (
	ErrProviderCreationFailed = &SessionError{Op: "provider", Err: fmt.Errorf("provider creation failed")}
)

// Tool errors
var (
	ErrToolExecutionFailed = &SessionError{Op: "tool", Err: fmt.Errorf("tool execution failed")}
)

// ============================================================================
// Structured Error Types
// ============================================================================

// SessionError represents an error with operation context.
// It provides structured information about what operation failed.
type SessionError struct {
	Op   string    // The operation that failed (e.g., "model_set", "save")
	Err  error     // The underlying error
	Kind ErrorKind // Categorization for programmatic dispatch
}

// ErrorKind classifies an error for structured handling.
type ErrorKind int

const (
	KindOther ErrorKind = iota
	KindModel
	KindQueue
	KindSession
	KindCommand
	KindInput
	KindProvider
	KindTool
)

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

// ErrorKind returns the kind of error for programmatic dispatch.
func (e *SessionError) ErrorKind() ErrorKind {
	return e.Kind
}

// NewSessionError creates a new SessionError with the given operation and error.
func NewSessionError(op string, err error) *SessionError {
	return &SessionError{Op: op, Err: err, Kind: inferKind(op)}
}

// NewSessionErrorf creates a new SessionError with a formatted error message.
func NewSessionErrorf(op, format string, args ...any) *SessionError {
	return &SessionError{Op: op, Err: fmt.Errorf(format, args...), Kind: inferKind(op)}
}

// Wrap wraps an existing error with operation context.
func Wrap(op string, err error) *SessionError {
	if err == nil {
		return nil
	}
	return &SessionError{Op: op, Err: err, Kind: inferKind(op)}
}

// Wrapf wraps an error with operation context and a formatted message.
func Wrapf(op string, err error, format string, args ...any) *SessionError {
	if err == nil {
		return nil
	}
	return &SessionError{Op: op, Err: fmt.Errorf(format+": %w", append(args, err)...), Kind: inferKind(op)}
}

// Common operation names for error context.
const (
	OpModelSet  = "model_set"
	OpModelLoad = "model_load"
	OpModel     = "model"

	OpQueueDel  = "taskqueue_del"
	OpQueueEdit = "taskqueue_edit"
	OpQueue     = "taskqueue"

	OpSave    = "save"
	OpLoad    = "load"
	OpSession = "session"

	OpCommand   = "command"
	OpCancel    = "cancel"
	OpCancelAll = "cancel_all"

	OpInput    = "input"
	OpProvider = "provider"
	OpStream   = "stream"
	OpTool     = "tool"
)

// inferKind maps common operation names to ErrorKind.
//
//nolint:gocyclo // Simple string-to-enum mapping, intentional list of cases
func inferKind(op string) ErrorKind {
	switch op {
	case OpModelSet, OpModelLoad, OpModel:
		return KindModel
	case OpQueueDel, OpQueueEdit, OpQueue:
		return KindQueue
	case OpSave, OpLoad, OpSession:
		return KindSession
	case OpCommand, OpCancel, OpCancelAll:
		return KindCommand
	case OpInput:
		return KindInput
	case OpProvider, OpStream:
		return KindProvider
	case OpTool:
		return KindTool
	default:
		return KindOther
	}
}
