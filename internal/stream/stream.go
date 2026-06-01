package stream

// Package stream defines the minimal IO abstraction and TLV encoding
// used between adaptors (terminal/plainio) and the core session.
// It intentionally stays small: SliceBuffer plus helpers for
// reading/writing framed Tag-Length-Value messages over io.Reader/io.Writer.

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"sync"
)

const (
	TagAssistantR = "AR" // Reasoning/thinking content
	TagAssistantT = "AT" // Assistant text output
	TagAssistantF = "AF" // JSON: id, type, name, input, status (function arguments)
	TagUserT      = "UT" // User text input
	TagUserF      = "UF" // JSON: id, output, status (function result)
	TagUserI      = "UI" // User image — DataURI: data:image/...;base64,...

	TagSystemMsg = "SM" // System message JSON: {"type":"...","data":{...}}
)

// SliceBuffer is an io.ReadWriteCloser that bridges slice-at-a-time writes
// with byte-at-a-time reads. Write sends each slice atomically via a
// channel; Read uses an internal buffer to reassemble slices into a
// byte stream so callers like io.ReadFull get continuous byte access
// without losing slice boundaries.
type SliceBuffer struct {
	ch        chan []byte
	buf       []byte
	closeOnce sync.Once
}

// NewSliceBuffer creates a SliceBuffer with the given channel buffer size.
func NewSliceBuffer(bufferSize int) *SliceBuffer {
	return &SliceBuffer{ch: make(chan []byte, bufferSize)}
}

// Close closes the input channel, causing Read to return EOF.
// It is safe to call Close multiple times.
func (b *SliceBuffer) Close() error {
	b.closeOnce.Do(func() { close(b.ch) })
	return nil
}

// Read implements io.Reader.
//
// Writes arrive as atomic slices via the channel; Read copies as much as
// fits into p and buffers the rest so callers like io.ReadFull see a
// continuous byte stream.
func (b *SliceBuffer) Read(p []byte) (n int, err error) {
	if len(b.buf) > 0 {
		n = copy(p, b.buf)
		b.buf = b.buf[n:]
		return n, nil
	}

	msg, ok := <-b.ch
	if !ok {
		return 0, io.EOF
	}

	b.buf = msg
	n = copy(p, b.buf)
	b.buf = b.buf[n:]
	return n, nil
}

// Write implements io.Writer. Each call sends p as a single atomic slice
// to the channel. Safe for concurrent use.
func (b *SliceBuffer) Write(p []byte) (int, error) {
	b.ch <- p
	return len(p), nil
}

// EncodeTLV creates a TLV-encoded byte slice.
// Format: [2-byte tag][4-byte length][value]
func EncodeTLV(tag string, value string) []byte {
	data := []byte(value)
	length := len(data)
	if length > maxMessageSize {
		length = maxMessageSize
		data = data[:maxMessageSize]
	}

	msg := make([]byte, 6+length)
	msg[0] = tag[0]
	msg[1] = tag[1]
	binary.BigEndian.PutUint32(msg[2:], uint32(length)) //nolint:gosec // G115: length is bounded by maxMessageSize
	copy(msg[6:], data)

	return msg
}

const maxMessageSize = 1<<31 - 1 // Max int32 to fit in uint32

// WriteTLV writes a TLV-encoded message to the writer.
func WriteTLV(output io.Writer, tag string, value string) error {
	_, err := output.Write(EncodeTLV(tag, value))
	return err
}

// ReadTLV reads a single TLV-framed message from input.
// It blocks until a full frame has been read or an error occurs.
func ReadTLV(input io.Reader) (string, string, error) {
	header := make([]byte, 6)
	if _, err := io.ReadFull(input, header); err != nil {
		return "", "", err
	}
	tag := string(header[0:2])
	length := binary.BigEndian.Uint32(header[2:])

	if length == 0 {
		return tag, "", nil
	}

	valueBuf := make([]byte, length)
	if _, err := io.ReadFull(input, valueBuf); err != nil {
		return "", "", err
	}

	return tag, string(valueBuf), nil
}

// NopInput is a Reader that always returns EOF.
type NopInput struct{}

func (n *NopInput) Read(_ []byte) (int, error) {
	return 0, io.EOF
}

// NopOutput is a Writer that discards all data.
type NopOutput struct{}

func (n *NopOutput) Write(p []byte) (int, error) {
	return len(p), nil
}

// FunctionData is the JSON payload for TagAssistantF (AF).
// Type discriminator:
//
//	"start" — tool name known, input placeholder
//	"call"  — full tool input available
type FunctionData struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Name  string `json:"name,omitempty"`
	Input string `json:"input,omitempty"`
}

// ToolResultData is the JSON payload for TagUserF (UF).
// Status is set to "success" or "failed" when the tool completes.
type ToolResultData struct {
	ID     string `json:"id"`
	Output string `json:"output"`
	Status string `json:"status,omitempty"`
}

// ReasoningData is the JSON payload for TagAssistantR delta values.
// Used for persistence when Anthropic's thinking block includes a signature.
// Text is the thinking content; Signature is Anthropic-specific and only
// present on thinking blocks.
type ReasoningData struct {
	Text      string `json:"text"`
	Signature string `json:"signature,omitempty"`
}

// ============================================================================
// TagSystemMsg (SM) — typed system messages
// Wire format: {"type":"...","data":{...}}
// ============================================================================

// SystemMsg is implemented by all TagSystemMsg payloads.
type SystemMsg interface {
	SystemMsgType() string
}

// systemMsgEnvelope is the wire format for TagSystemMsg.
type systemMsgEnvelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// WriteSystemMsg marshals msg as a TagSystemMsg TLV frame.
func WriteSystemMsg(w io.Writer, msg SystemMsg) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	env, err := json.Marshal(systemMsgEnvelope{Type: msg.SystemMsgType(), Data: data})
	if err != nil {
		return err
	}
	return WriteTLV(w, TagSystemMsg, string(env))
}

// ErrorMsg is a system error message (type "error").
type ErrorMsg struct {
	Text string `json:"text"`
}

func (ErrorMsg) SystemMsgType() string { return "error" }

// NotifyMsg is a system notification (type "notify").
type NotifyMsg struct {
	Text string `json:"text"`
}

func (NotifyMsg) SystemMsgType() string { return "notify" }

// ToolConfirmMsg is sent when a tool call needs user confirmation
// (type "tool_confirm").
//
// Request (agent → adaptor):
//
//	SM {"type":"tool_confirm","data":{"id":"<toolCallID>"}}
//
// Response (adaptor → agent):
//
//	SM {"type":"tool_confirm","data":{"id":"<toolCallID>","allowed":true|false}}
type ToolConfirmMsg struct {
	ID      string `json:"id"`
	Allowed *bool  `json:"allowed,omitempty"`
}

func (ToolConfirmMsg) SystemMsgType() string { return "tool_confirm" }
