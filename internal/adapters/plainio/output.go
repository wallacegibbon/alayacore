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

// stdoutOutput implements io.Writer.
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

// ErrorChannel returns a channel that is closed when an SE (system error)
// tag is received. It can be used in a select to react to errors without
// a dedicated goroutine.
func (o *stdoutOutput) ErrorChannel() <-chan struct{} {
	return o.errorCh
}

// HasError returns true if any TagSystemMsg with type "error" was ever received.
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
	case stream.TagAssistantT, stream.TagAssistantR:
		o.handleTextDelta(tag, value)

	case stream.TagUserT:
		o.emitSeparator(tag)
		fmt.Fprintf(o.writer, "> %s", value)

	case stream.TagSystemMsg:
		o.handleSystemMsg(value)

	case stream.TagAssistantF:
		var fd stream.ToolUseData
		if err := json.Unmarshal([]byte(value), &fd); err != nil {
			return
		}
		// Ignore placeholder frames — only render when full input is available.
		if fd.IsPlaceholder {
			return
		}
		if o.lastTag != "" {
			fmt.Fprintln(o.writer)
		}
		o.lastTag = tag
		o.lastStreamID = ""
		fmt.Fprintf(o.writer, "%s", formatToolUse(fd.Name, fd.Input))

	case stream.TagUserF:
		// Suppress tool result content in plainio; do not update lastTag.

	case stream.TagUserI:
		o.emitSeparator(tag)
		fmt.Fprint(o.writer, "[image]")

	default:
		o.emitSeparator(tag)
		fmt.Fprintf(o.writer, "[unknown-tag:%s %s]", tag, value)
	}
}

// handleTextDelta handles AT (assistant text) and AR (reasoning text) tags.
// It prints a separator when transitioning between different tag groups or
// stream IDs, then prints the content delta.
func (o *stdoutOutput) handleTextDelta(tag, value string) {
	id, content, _ := stream.UnwrapDelta(value)
	// Use tag+id as the stream key so text and reasoning from the same step
	// are treated as separate streams.
	streamKey := tag + id
	// When id is "" (replayed from session file, no NUL prefix),
	// we just track it as-is — no stream transition to detect.
	if o.lastTag != "" && (o.lastTag != stream.TagAssistantT && o.lastTag != stream.TagAssistantR) {
		// Transitioning from a different tag group → separator
		fmt.Fprintln(o.writer)
	} else if o.lastStreamID != "" && streamKey != o.lastStreamID {
		// Same group but different stream → separator
		fmt.Fprintln(o.writer)
	}
	o.lastTag = tag
	o.lastStreamID = streamKey
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

// formatToolUse formats a tool call header for display (name + key args, no content).
func formatToolUse(name, input string) string {
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
	if args.IgnoreCase {
		parts = append(parts, "ignoring case")
	}
	if args.MaxLines > 0 {
		parts = append(parts, fmt.Sprintf("limit %d", args.MaxLines))
	}

	return fmt.Sprintf("[search_content: %s]", strings.Join(parts, ", "))
}

// handleSystemMsg processes a TagSystemMsg frame.
// Handles error, notify, task, and tool_confirm system messages.
// Task completion transitions print a trailing blank line between tasks.
func (o *stdoutOutput) handleSystemMsg(value string) {
	var env struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(value), &env); err != nil {
		return
	}
	switch env.Type {
	case "error":
		var m struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(env.Data, &m) == nil {
			o.emitSeparator("")
			fmt.Fprintf(o.writer, "[error: %s]", m.Text)
			o.hasError.Store(true)
			if o.errorClosed.CompareAndSwap(false, true) {
				close(o.errorCh)
			}
		}
	case "notify":
		var m struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(env.Data, &m) == nil {
			o.emitSeparator("")
			fmt.Fprintf(o.writer, "[%s]", m.Text)
		}
	case "task":
		var m struct {
			InProgress bool `json:"in_progress"`
		}
		if json.Unmarshal(env.Data, &m) == nil {
			if o.inProgress.Load() && !m.InProgress {
				fmt.Fprintln(o.writer)
				o.lastTag = ""
				o.lastStreamID = ""
			}
			o.inProgress.Store(m.InProgress)
		}
	case "tool_confirm":
		var m struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(env.Data, &m) != nil || m.ID == "" {
			return
		}
		o.emitSeparator("")
		fmt.Fprintf(o.writer, "[tool_confirm: allow tool %q to run?]", m.ID)
	}
}
