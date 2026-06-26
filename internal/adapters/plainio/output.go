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
// so a mutex protects the buffer, tag/history-ID state, and the close-once
// guard for errorCh. See stream/doc.go for the full contract.
type stdoutOutput struct {
	writer        io.Writer
	mu            sync.Mutex // protects buf, lastTag, lastHistoryID, errorClosed
	buf           []byte
	inProgress    atomic.Bool
	hasError      atomic.Bool
	errorClosed   atomic.Bool // true once errorCh has been closed
	errorCh       chan struct{}
	lastTag       string
	lastHistoryID string
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
		_, content, ok := stream.UnwrapDelta(value)
		if !ok {
			content = value
		}
		o.emitSeparator(tag)
		fmt.Fprintf(o.writer, "> %s\n", content)

	case stream.TagSystemMsg:
		o.handleSystemMsg(value)

	case stream.TagAssistantF:
		_, payload, ok := stream.UnwrapDelta(value)
		if !ok {
			payload = value
		}
		if o.lastTag != "" {
			fmt.Fprintln(o.writer)
		}
		o.lastTag = tag
		o.lastHistoryID = ""
		// Show complete tool call JSON.
		fmt.Fprintf(o.writer, "%s\n", payload)

	case stream.TagUserF:
		_, payload, ok := stream.UnwrapDelta(value)
		if !ok {
			payload = value
		}
		// Show complete tool result JSON.
		if o.lastTag != "" && o.lastTag != tag {
			fmt.Fprintln(o.writer)
		}
		o.lastTag = tag
		o.lastHistoryID = ""
		fmt.Fprintf(o.writer, "%s\n", payload)

	case stream.TagUserI, stream.TagUserV, stream.TagUserA, stream.TagUserD:
		o.handleMediaTag(tag, value)

	default:
		o.emitSeparator(tag)
		fmt.Fprintf(o.writer, "[unknown-tag:%s %s]", tag, value)
	}
}

// handleTextDelta handles AT (assistant text) and AR (reasoning text) tags.
// It prints a separator when transitioning between different tags or
// history IDs, then prints the content delta.
func (o *stdoutOutput) handleTextDelta(tag, value string) {
	id, content, _ := stream.UnwrapDelta(value)
	// When id is "" (replayed from session file, no NUL prefix),
	// we just track it as-is — no history transition to detect.
	if o.lastHistoryID != "" && o.lastTag != tag {
		// Transitioning from a different tag → separator
		fmt.Fprintln(o.writer)
	} else if o.lastHistoryID != "" && id != o.lastHistoryID {
		// Same tag but different history ID → separator
		fmt.Fprintln(o.writer)
	}
	o.lastTag = tag
	o.lastHistoryID = id
	fmt.Fprint(o.writer, content)
	if id == "" {
		fmt.Fprintln(o.writer)
	}
}

// emitSeparator prints a newline if the previous visible tag differs from the
// new tag and the previous frame was streamed (had a non-empty history ID).
// It updates lastTag to the new tag.
// handleMediaTag prints a media label (image/video/audio/document).
func (o *stdoutOutput) handleMediaTag(tag, value string) {
	stream.UnwrapDelta(value)
	o.emitSeparator(tag)
	label := map[string]string{
		stream.TagUserI: "image",
		stream.TagUserV: "video",
		stream.TagUserA: "audio",
		stream.TagUserD: "document",
	}[tag]
	fmt.Fprintf(o.writer, "[%s]\n", label)
}

func (o *stdoutOutput) emitSeparator(tag string) {
	if o.lastHistoryID != "" && o.lastTag != "" && o.lastTag != tag {
		fmt.Fprintln(o.writer)
	}
	o.lastTag = tag
	o.lastHistoryID = ""
}

// handleSystemMsg processes a TagSystemMsg frame.
// Handles error, notify, task, and tool_confirm system messages.
// Task completion transitions print a trailing blank line between tasks.
func (o *stdoutOutput) handleSystemMsg(value string) {
	env, err := stream.ParseSystemMsg(value)
	if err != nil {
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
			o.lastHistoryID = ""
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
			o.lastHistoryID = ""
		}
	case "task":
		var m struct {
			InProgress bool `json:"in_progress"`
		}
		if json.Unmarshal(env.Data, &m) == nil {
			if o.inProgress.Load() && !m.InProgress {
				fmt.Fprintln(o.writer)
				o.lastTag = ""
				o.lastHistoryID = ""
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
		o.lastHistoryID = ""
	}
}
