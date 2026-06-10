package plainio

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"

	"github.com/alayacore/alayacore/internal/stream"
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
		fmt.Fprintf(o.writer, "> %s\n", value)

	case stream.TagSystemMsg:
		o.handleSystemMsg(value)

	case stream.TagAssistantF:
		var fd stream.ToolUseData
		if err := json.Unmarshal([]byte(value), &fd); err != nil {
			return
		}
		if o.lastTag != "" {
			fmt.Fprintln(o.writer)
		}
		o.lastTag = tag
		o.lastStreamID = ""
		switch {
		case fd.Name != "" && len(fd.Input) == 0:
			// Start frame: tool name only.
			fmt.Fprintf(o.writer, "%s\n", fd.Name)
		case fd.Name != "":
			// Combined frame (session load): name and input.
			fmt.Fprintf(o.writer, "%s\n%s\n", fd.Name, fd.Input)
		default:
			// Input frame: arguments only.
			fmt.Fprintf(o.writer, "%s\n", fd.Input)
		}

	case stream.TagUserF:
		// Suppress tool result content in plainio; do not update lastTag.

	case stream.TagUserI:
		o.emitSeparator(tag)
		fmt.Fprintf(o.writer, "[image]\n")

	default:
		o.emitSeparator(tag)
		fmt.Fprintf(o.writer, "[unknown-tag:%s %s]", tag, value)
	}
}

// handleTextDelta handles AT (assistant text) and AR (reasoning text) tags.
// It prints a separator when transitioning between different tags or
// stream IDs, then prints the content delta.
func (o *stdoutOutput) handleTextDelta(tag, value string) {
	id, content, _ := stream.UnwrapDelta(value)
	// When id is "" (replayed from session file, no NUL prefix),
	// we just track it as-is — no stream transition to detect.
	if o.lastStreamID != "" && o.lastTag != tag {
		// Transitioning from a different tag → separator
		fmt.Fprintln(o.writer)
	} else if o.lastStreamID != "" && id != o.lastStreamID {
		// Same tag but different stream → separator
		fmt.Fprintln(o.writer)
	}
	o.lastTag = tag
	o.lastStreamID = id
	fmt.Fprint(o.writer, content)
	if id == "" {
		fmt.Fprintln(o.writer)
	}
}

// emitSeparator prints a newline if the previous visible tag differs from the
// new tag and the previous frame was streamed (had a non-empty stream ID).
// It updates lastTag to the new tag.
func (o *stdoutOutput) emitSeparator(tag string) {
	if o.lastStreamID != "" && o.lastTag != "" && o.lastTag != tag {
		fmt.Fprintln(o.writer)
	}
	o.lastTag = tag
	o.lastStreamID = ""
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
			fmt.Fprintf(o.writer, "\n[error: %s]\n", m.Text)
			o.lastTag = ""
			o.lastStreamID = ""
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
			fmt.Fprintf(o.writer, "\n[%s]\n", m.Text)
			o.lastTag = ""
			o.lastStreamID = ""
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
		fmt.Fprintf(o.writer, "\n[tool_confirm: allow tool %q to run?]\n", m.ID)
		o.lastTag = ""
		o.lastStreamID = ""
	}
}
