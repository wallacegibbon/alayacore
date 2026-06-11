package tools

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
)

func TestExecuteCommandNormalCompletion(t *testing.T) {
	content, err := executeCommand(context.Background(), executeCommandInput{
		Command: "echo hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractText(content)
	if text != "hello\n" {
		t.Errorf("expected %q, got %q", "hello\n", text)
	}
}

func TestExecuteCommandExitError(t *testing.T) {
	_, err := executeCommand(context.Background(), executeCommandInput{
		Command: "exit 42",
	})
	if err == nil {
		t.Fatal("expected error for exit 42")
	}
	if err.Error() == "" {
		t.Error("expected non-empty error message")
	}
}

func TestExecuteCommandCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := executeCommand(ctx, executeCommandInput{
			Command: "sleep 60",
		})
		done <- err
	}()

	time.Sleep(500 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error for canceled command")
		}
		if !strings.HasPrefix(err.Error(), "canceled") {
			t.Errorf("expected message to start with 'canceled', got %q", err.Error())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("command was not canceled within timeout")
	}
}

func TestExecuteCommandTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := executeCommand(ctx, executeCommandInput{
			Command: "sleep 60",
		})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error for timed out command")
		}
		msg := err.Error()
		if !strings.HasPrefix(msg, "canceled") && !strings.HasPrefix(msg, "timed out") {
			t.Errorf("expected message to start with 'canceled' or 'timed out', got %q", msg)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("command was not terminated within timeout")
	}
}

func TestExecuteCommandWorkingDir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "alayacore-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	originalWd, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(originalWd)

	content, err := executeCommand(context.Background(), executeCommandInput{
		Command: "pwd",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractText(content)
	if text != tmpDir+"\n" {
		t.Errorf("expected %q, got %q", tmpDir+"\n", text)
	}
}

func TestHandleCommandCompletion(t *testing.T) {
	tests := []struct {
		name       string
		execErr    error
		wantError  bool
		wantInText string
	}{
		{
			name:       "success with output",
			execErr:    nil,
			wantError:  false,
			wantInText: "hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := bytes.NewBufferString("hello")
			stderr := &bytes.Buffer{}

			content, err := handleCommandOutput(stdout, stderr, 0, tt.execErr)

			if tt.wantError {
				if err == nil {
					t.Error("expected error")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				text := extractText(content)
				if text != tt.wantInText {
					t.Errorf("expected %q, got %q", tt.wantInText, text)
				}
			}
		})
	}
}

// extractText is a test helper to get the text from a ContentPart slice.
func extractText(content []llm.ContentPart) string {
	for _, p := range content {
		if tp, ok := p.(*llm.TextPart); ok {
			return tp.Text
		}
	}
	return ""
}
