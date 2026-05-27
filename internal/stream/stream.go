package stream

// Package stream defines the minimal IO abstraction and TLV encoding
// used between adaptors (terminal/plainio) and the core session.
// It intentionally stays small: SliceReadWriter plus helpers for
// reading/writing framed Tag-Length-Value messages over io.Reader/io.Writer.

import (
	"encoding/binary"
	"io"
	"sync"
)

const (
	TagTextUser      = "TU" // User text input
	TagTextAssistant = "TA" // Assistant text output
	TagTextReasoning = "TR" // Reasoning/thinking content

	TagFunctionCall   = "FC" // JSON: id, name, input (display + persistence)
	TagFunctionResult = "FR" // JSON: id, output      (display + persistence)
	TagFunctionState  = "FS" // JSON: id, status      (pending/success/error)

	TagSystemError  = "SE" // Error message string
	TagSystemNotify = "SN" // Notification message string
	TagSystemData   = "SD" // Complex data JSON (queue status, model info, etc.)
)

// SliceReadWriter is an io.ReadWriter that bridges slice-at-a-time writes
// with byte-at-a-time reads. Write sends each slice atomically via a
// channel; Read uses an internal buffer to reassemble slices into a
// byte stream so callers like io.ReadFull get continuous byte access
// without losing slice boundaries.
type SliceReadWriter struct {
	ch        chan []byte
	buf       []byte
	closeOnce sync.Once
}

// NewSliceReadWriter creates a SliceReadWriter with the given channel buffer size.
func NewSliceReadWriter(bufferSize int) *SliceReadWriter {
	return &SliceReadWriter{ch: make(chan []byte, bufferSize)}
}

// Close closes the input channel, causing Read to return EOF.
// It is safe to call Close multiple times.
func (rw *SliceReadWriter) Close() error {
	rw.closeOnce.Do(func() { close(rw.ch) })
	return nil
}

// Read implements io.Reader.
//
// Writes arrive as atomic slices via the channel; Read copies as much as
// fits into p and buffers the rest so callers like io.ReadFull see a
// continuous byte stream.
func (rw *SliceReadWriter) Read(p []byte) (n int, err error) {
	if len(rw.buf) > 0 {
		n = copy(p, rw.buf)
		rw.buf = rw.buf[n:]
		return n, nil
	}

	msg, ok := <-rw.ch
	if !ok {
		return 0, io.EOF
	}

	rw.buf = msg
	n = copy(p, rw.buf)
	rw.buf = rw.buf[n:]
	return n, nil
}

// Write implements io.Writer. Each call sends p as a single atomic slice
// to the channel. Safe for concurrent use.
func (rw *SliceReadWriter) Write(p []byte) (int, error) {
	rw.ch <- p
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

// ToolCallData is the JSON payload for TagFunctionCall.
type ToolCallData struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input string `json:"input"`
}

// ToolResultData is the JSON payload for TagFunctionResult.
type ToolResultData struct {
	ID     string `json:"id"`
	Output string `json:"output"`
}

// ToolStateData is the JSON payload for TagFunctionState.
type ToolStateData struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// ReasoningData is the JSON payload for TagTextReasoning delta values.
// Used for persistence when Anthropic's thinking block includes a signature.
// Text is the thinking content; Signature is Anthropic-specific and only
// present on thinking blocks.
type ReasoningData struct {
	Text      string `json:"text"`
	Signature string `json:"signature,omitempty"`
}
