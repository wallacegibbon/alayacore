package stream

// Package stream defines the minimal IO abstraction and TLV encoding
// used between adapters (terminal/plainio) and the core session.
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
	TagUserV      = "UV" // User video — DataURI: data:video/...;base64,...
	TagUserA      = "UA" // User audio — DataURI: data:audio/...;base64,...
	TagUserD      = "UD" // User document — DataURI: data:application/...;base64,...

	TagSystemMsg = "SM" // System message JSON: {"type":"...","data":{...}}
)

// SliceBuffer is an io.ReadWriteCloser that bridges slice-at-a-time writes
// with byte-at-a-time reads. Write copies each slice and sends it
// atomically via a channel (the caller, e.g. io.Copy, may reuse its
// buffer); Read uses an internal buffer to reassemble slices into a
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

// Write implements io.Writer. Each call sends a copy of p as a single
// atomic slice to the channel. Safe for concurrent use.
// A copy is required because the caller (e.g. io.Copy) may reuse the
// underlying buffer on subsequent writes, and the data must remain valid
// until the reader goroutine consumes it from the channel.
func (b *SliceBuffer) Write(p []byte) (int, error) {
	buf := make([]byte, len(p))
	copy(buf, p)
	b.ch <- buf
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

// ToolInputData is the JSON payload for TagAssistantF (AF).
// A frame with a non-empty Name and empty Input is a preliminary
// "start" frame that announces the tool name. All other frames
// carry the actual tool arguments.
type ToolInputData struct {
	ID    string          `json:"id"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// ToolOutputData is the JSON payload for TagUserF (UF).
// Output is a JSON array of content blocks (text, image, etc.).
// IsError indicates whether the tool completed with an error.
type ToolOutputData struct {
	ID      string          `json:"id"`
	Output  json.RawMessage `json:"output"`
	IsError bool            `json:"is_error,omitempty"`
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
type SystemMsgEnvelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// ParseSystemMsg parses a TagSystemMsg value into its type and payload.
// Returns the envelope on success, or an error if the JSON is malformed.
func ParseSystemMsg(value string) (SystemMsgEnvelope, error) {
	var env SystemMsgEnvelope
	if err := json.Unmarshal([]byte(value), &env); err != nil {
		return SystemMsgEnvelope{}, err
	}
	return env, nil
}

// ParseSystemMsgType extracts just the type from a TagSystemMsg value.
// Returns the type string and whether parsing succeeded.
func ParseSystemMsgType(value string) (string, bool) {
	env, err := ParseSystemMsg(value)
	return env.Type, err == nil
}

// WriteSystemMsg marshals msg as a TagSystemMsg TLV frame.
func WriteSystemMsg(w io.Writer, msg SystemMsg) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	env, err := json.Marshal(SystemMsgEnvelope{Type: msg.SystemMsgType(), Data: data})
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
// Request (agent -> adapter):
//
//	SM {"type":"tool_confirm","data":{"id":"<toolUseID>"}}
//
// Response (adapter -> agent):
//
//	SM {"type":"tool_confirm","data":{"id":"<toolUseID>","allowed":true|false}}
type ToolConfirmMsg struct {
	ID      string `json:"id"`
	Allowed *bool  `json:"allowed,omitempty"`
}

func (ToolConfirmMsg) SystemMsgType() string { return "tool_confirm" }
