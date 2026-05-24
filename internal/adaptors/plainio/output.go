package plainio

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/alayacore/alayacore/internal/stream"
	"github.com/alayacore/alayacore/internal/tools"
)

// systemInfo mirrors the SystemInfo JSON from the agent package.
type systemInfo struct {
	InProgress bool `json:"in_progress"`
}

// stdoutOutput implements stream.Output.
// It parses TLV messages and prints human-readable text to stdout.
//
// Concurrency: the session writes from two goroutines (task and run),
// so a mutex protects the buffer, tag/stream-ID state, and the close-once
// guard for errorCh. See stream/doc.go for the full contract.
type stdoutOutput struct {
	writer       io.Writer
	mu           sync.Mutex // protects buf, lastTag, lastStreamID, errorClosed
	buf          []byte
	inProgress   atomic.Bool
	hasError     atomic.Bool
	errorClosed  atomic.Bool // true once errorCh has been closed
	errorCh      chan struct{}
	lastTag      string
	lastStreamID string
}

func newStdoutOutput() *stdoutOutput {
	return &stdoutOutput{
		writer:  os.Stdout,
		errorCh: make(chan struct{}),
	}
}

func (o *stdoutOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	o.buf = append(o.buf, p...)
	o.processBuffer()
	o.mu.Unlock()
	return len(p), nil
}

func (o *stdoutOutput) WriteString(s string) (int, error) {
	return o.Write([]byte(s))
}

func (o *stdoutOutput) Flush() error {
	return nil
}

// ErrorChannel returns a channel that is closed when an SE (system error)
// tag is received. It can be used in a select to react to errors without
// a dedicated goroutine.
func (o *stdoutOutput) ErrorChannel() <-chan struct{} {
	return o.errorCh
}

// HasError returns true if any SE tag was ever received.
func (o *stdoutOutput) HasError() bool {
	return o.hasError.Load()
}

// processBuffer parses and prints complete TLV frames from the buffer.
func (o *stdoutOutput) processBuffer() {
	for len(o.buf) >= 6 {
		tag := string(o.buf[0:2])
		length := int(binary.BigEndian.Uint32(o.buf[2:6]))
		if len(o.buf) < 6+length {
			break
		}
		value := string(o.buf[6 : 6+length])
		o.buf = o.buf[6+length:]
		o.printMessage(tag, value)
	}
}

func (o *stdoutOutput) printMessage(tag string, value string) {
	o.handleTag(tag, value)
}

func (o *stdoutOutput) handleTag(tag, value string) {
	switch tag {
	case stream.TagTextAssistant, stream.TagTextReasoning:
		o.handleTextDelta(tag, value)

	case stream.TagTextUser:
		o.emitSeparator(tag)
		fmt.Fprintf(o.writer, "> %s", value)

	case stream.TagSystemError:
		o.emitSeparator(tag)
		fmt.Fprintf(o.writer, "[error: %s]", value)
		o.hasError.Store(true)
		if o.errorClosed.CompareAndSwap(false, true) {
			close(o.errorCh)
		}

	case stream.TagSystemNotify:
		o.emitSeparator(tag)
		fmt.Fprintf(o.writer, "[%s]", value)

	case stream.TagFunctionCall:
		if o.lastTag != "" {
			fmt.Fprintln(o.writer)
		}
		o.lastTag = tag
		o.lastStreamID = ""
		o.printFunctionCall(value)

	case stream.TagFunctionResult:
		// Suppress tool result content; do not update lastTag.

	case stream.TagFunctionState:
		// Skip state indicators for plainio

	case stream.TagSystemData:
		o.handleSystemData(value)

	default:
		o.emitSeparator(tag)
		fmt.Fprintf(o.writer, "[unknown-tag:%s %s]", tag, value)
	}
}

// handleTextDelta handles TA (assistant text) and TR (reasoning text) tags.
// It prints a separator when transitioning between different tag groups or
// stream IDs, then prints the content delta.
func (o *stdoutOutput) handleTextDelta(tag, value string) {
	id, content, _ := stream.UnwrapDelta(value)
	// When id is "" (replayed from session file, no NUL prefix),
	// we just track it as-is — no stream transition to detect.
	if o.lastTag != "" && (o.lastTag != stream.TagTextAssistant && o.lastTag != stream.TagTextReasoning) {
		// Transitioning from a different tag group → separator
		fmt.Fprintln(o.writer)
	} else if o.lastStreamID != "" && id != o.lastStreamID {
		// Same group but different stream → separator
		fmt.Fprintln(o.writer)
	}
	o.lastTag = tag
	o.lastStreamID = id
	fmt.Fprint(o.writer, content)
}

// emitSeparator prints a newline if the previous visible tag differs from the
// new tag. It updates lastTag to the new tag.
func (o *stdoutOutput) emitSeparator(tag string) {
	if o.lastTag != "" && o.lastTag != tag {
		fmt.Fprintln(o.writer)
	}
	o.lastTag = tag
	o.lastStreamID = ""
}

func (o *stdoutOutput) printFunctionCall(value string) {
	var tc struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Input string `json:"input"`
	}
	if err := json.Unmarshal([]byte(value), &tc); err != nil {
		return
	}
	fmt.Fprintf(o.writer, "%s", formatToolCall(tc.Name, tc.Input))
}

// formatToolCall formats a tool call header for display (name + key args, no content).
func formatToolCall(name, input string) string {
	switch name {
	case "execute_command":
		return formatExecuteCommand(input)
	case "read_file":
		return formatReadFile(input)
	case "write_file":
		return formatWriteFile(input)
	case "edit_file":
		return formatEditFile(input)
	case "search_content":
		return formatSearchContent(input)
	default:
		return fmt.Sprintf("[%s]", name)
	}
}

func formatExecuteCommand(input string) string {
	// Use anonymous struct since executeCommandInput is unexported
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return "[execute_command]"
	}
	return fmt.Sprintf("[execute_command: %s]", args.Command)
}

func formatReadFile(input string) string {
	var args tools.ReadFileInput
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return "[read_file]"
	}
	parts := []string{args.Path}
	if args.StartLine > 0 {
		parts = append(parts, fmt.Sprintf("%d", args.StartLine))
	}
	if args.EndLine > 0 {
		parts = append(parts, fmt.Sprintf("%d", args.EndLine))
	}
	return fmt.Sprintf("[read_file: %s]", strings.Join(parts, ", "))
}

func formatWriteFile(input string) string {
	var args tools.WriteFileInput
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return "[write_file]"
	}
	return fmt.Sprintf("[write_file: %s]", args.Path)
}

func formatEditFile(input string) string {
	var args tools.EditFileInput
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return "[edit_file]"
	}
	return fmt.Sprintf("[edit_file: %s]", args.Path)
}

func formatSearchContent(input string) string {
	var args tools.SearchContentInput
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return "[search_content]"
	}

	var parts []string

	// Pattern and path
	part := args.Pattern
	if args.Path != "" {
		part += " in " + args.Path
	}
	parts = append(parts, part)

	// FileType and/or Glob
	switch {
	case args.FileType != "" && args.Glob != "":
		parts = append(parts, fmt.Sprintf("for %s files (%s)", args.FileType, args.Glob))
	case args.FileType != "":
		parts = append(parts, fmt.Sprintf("for %s files", args.FileType))
	case args.Glob != "":
		parts = append(parts, fmt.Sprintf("matching %s", args.Glob))
	}

	// Modifiers
	if args.IgnoreCase == "true" {
		parts = append(parts, "ignoring case")
	}
	if args.MaxLines > 0 {
		parts = append(parts, fmt.Sprintf("limit %d", args.MaxLines))
	}

	return fmt.Sprintf("[search_content: %s]", strings.Join(parts, ", "))
}

// handleSystemData detects task completion transitions and prints a trailing newline.
func (o *stdoutOutput) handleSystemData(value string) {
	var info systemInfo
	if err := json.Unmarshal([]byte(value), &info); err != nil {
		return
	}
	if o.inProgress.Load() && !info.InProgress {
		fmt.Fprintln(o.writer)
		o.lastTag = ""
		o.lastStreamID = ""
	}
	o.inProgress.Store(info.InProgress)
}
