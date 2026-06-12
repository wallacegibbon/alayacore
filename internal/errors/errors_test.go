package errors

import (
	"errors"
	"testing"
)

func TestSessionError(t *testing.T) {
	t.Run("Error includes Op prefix", func(t *testing.T) {
		err := &SessionError{Op: "test", Err: errors.New("underlying error")}
		if err.Error() != "test: underlying error" {
			t.Errorf("Error() = %q, want %q", err.Error(), "test: underlying error")
		}
	})

	t.Run("Error with nil Err returns op", func(t *testing.T) {
		err := &SessionError{Op: "test", Err: nil}
		if err.Error() != "test" {
			t.Errorf("Error() = %q, want %q", err.Error(), "test")
		}
	})

	t.Run("Error without Op returns message only", func(t *testing.T) {
		err := &SessionError{Op: "", Err: errors.New("something failed")}
		if err.Error() != "something failed" {
			t.Errorf("Error() = %q, want %q", err.Error(), "something failed")
		}
	})

	t.Run("Unwrap returns underlying error", func(t *testing.T) {
		underlying := errors.New("underlying error")
		err := &SessionError{Op: "test", Err: underlying}
		if err.Unwrap() != underlying {
			t.Errorf("Unwrap() = %v, want %v", err.Unwrap(), underlying)
		}
	})

	t.Run("Operation returns op", func(t *testing.T) {
		err := &SessionError{Op: "model_set", Err: errors.New("error")}
		if err.Operation() != "model_set" {
			t.Errorf("Operation() = %q, want %q", err.Operation(), "model_set")
		}
	})
}

func TestNewSessionError(t *testing.T) {
	err := NewSessionErrorf("save", "disk full")
	if err.Op != "save" {
		t.Errorf("Op = %q, want %q", err.Op, "save")
	}
	if err.Error() != "save: disk full" {
		t.Errorf("Error() = %q, want %q", err.Error(), "save: disk full")
	}
}

func TestNewSessionErrorf(t *testing.T) {
	err := NewSessionErrorf("model_set", "model %s not found", "gpt-4")
	if err.Op != "model_set" {
		t.Errorf("Op = %q, want %q", err.Op, "model_set")
	}
	if err.Error() != "model_set: model gpt-4 not found" {
		t.Errorf("Error() = %q, want %q", err.Error(), "model_set: model gpt-4 not found")
	}
}

func TestWrap(t *testing.T) {
	underlying := errors.New("underlying error")
	err := Wrap("cancel", underlying)
	if err.Op != "cancel" {
		t.Errorf("Op = %q, want %q", err.Op, "cancel")
	}
	if err.Unwrap() != underlying {
		t.Errorf("Unwrap() = %v, want %v", err.Unwrap(), underlying)
	}
}

func TestWrapf(t *testing.T) {
	underlying := errors.New("underlying error")
	err := Wrapf("save", underlying, "failed to save %s", "file.txt")
	if err.Op != "save" {
		t.Errorf("Op = %q, want %q", err.Op, "save")
	}
	// The error message should contain both the formatted message and the underlying error
	errMsg := err.Error()
	if errMsg == "" {
		t.Error("Error() should not be empty")
	}
	// Check that we can unwrap to get the underlying error
	if !errors.Is(err, underlying) {
		t.Error("errors.Is() should return true for underlying error")
	}
}

func TestDomainErrors(t *testing.T) {
	// Sentinel errors carry no Op — operation context is added by Wrap/Wrapf.
	tests := []struct {
		name string
		err  error
	}{
		{"ErrModelNotFound", ErrModelNotFound},
		{"ErrModelManagerNotInitialized", ErrModelManagerNotInitialized},
		{"ErrNoModelFilePath", ErrNoModelFilePath},
		{"ErrQueueItemNotFound", ErrQueueItemNotFound},
		{"ErrNoSessionFile", ErrNoSessionFile},
		{"ErrEmptyCommand", ErrEmptyCommand},
		{"ErrNothingToCancel", ErrNothingToCancel},
		{"ErrInvalidInputTag", ErrInvalidInputTag},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionErr, ok := tt.err.(*SessionError)
			if !ok {
				t.Fatalf("expected *SessionError, got %T", tt.err)
			}
			if sessionErr.Operation() != "" {
				t.Errorf("sentinel %s should have empty Op, got %q", tt.name, sessionErr.Operation())
			}
			if sessionErr.Error() == "" {
				t.Errorf("sentinel %s Error() should not be empty", tt.name)
			}
		})
	}
}
