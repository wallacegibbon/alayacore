package stream

// Package stream defines the minimal IO abstraction and TLV encoding
// used between adaptors (terminal/plainio) and the core session.
// It intentionally stays small: ChanInput plus helpers for reading/writing
// framed Tag-Length-Value messages over standard io.Reader/io.Writer.

import (
	"encoding/binary"
	"io"
	"sync"
)

// Message tags for TLV protocol (2-byte tags).
const (
	// Text content tags
	TagTextUser      = "TU" // User text input
	TagTextAssistant = "TA" // Assistant text output
	TagTextReasoning = "TR" // Reasoning/thinking content

	// Function/tool tags
	TagFunctionCall   = "FC" // Function call (JSON: id, name, input) - for both display and persistence
	TagFunctionResult = "FR" // Function result (JSON: id, output) - for both display and persistence
	TagFunctionState  = "FS" // Function state indicator (pending/success/error)

	// System tags
	TagSystemError  = "SE" // System error messages
	TagSystemNotify = "SN" // System notification messages (simple string)
	TagSystemData   = "SD" // System data messages (complex data, queue status, model info, etc.)
)

// ChanInput implements io.Reader using a channel of raw TLV-encoded messages.
type ChanInput struct {
	ch        chan []byte
	buf       []byte
	closeOnce sync.Once
}

// NewChanInput creates a ChanInput with the given buffer size.
func NewChanInput(bufferSize int) *ChanInput {
	return &ChanInput{ch: make(chan []byte, bufferSize)}
}

// Close closes the input channel, causing Read to return EOF.
// It is safe to call Close multiple times.
func (i *ChanInput) Close() error {
	i.closeOnce.Do(func() { close(i.ch) })
	return nil
}

// Read implements io.Reader. Returns io.EOF when the channel is closed.
//
// The buffer (i.buf) bridges two data models:
//   - ChanInput.Write sends chunks of bytes atomically into the channel
//     (a "slice stream" — each receive gives one complete chunk from a
//     single Write call).
//   - Callers like ReadTLV use io.ReadFull, which reads byte-by-byte in
//     arbitrary-sized chunks (a "byte stream" — 6 bytes for header, then N
//     bytes for value).
//
// Without the buffer, a single channel receive could produce a 500-byte
// chunk, but the caller might only ask for 6 bytes. The remaining 494
// bytes would be lost — corrupting the next read with the tail of the
// previous chunk.
//
// The buffer saves the unused portion and returns it on subsequent calls,
// only receiving from the channel when the buffer is fully drained.
func (i *ChanInput) Read(p []byte) (n int, err error) {
	if len(i.buf) > 0 {
		n = copy(p, i.buf)
		i.buf = i.buf[n:]
		return n, nil
	}

	msg, ok := <-i.ch
	if !ok {
		return 0, io.EOF
	}

	i.buf = msg
	n = copy(p, i.buf)
	i.buf = i.buf[n:]
	return n, nil
}

// Write implements io.Writer. It sends data to the input channel as an
// atomic slice. Data is typically a TLV-encoded frame (produced by
// EncodeTLV / WriteTLV), but no framing is enforced — any bytes are
// accepted and delivered atomically to Read.
//
// Safe for concurrent use — the channel handles synchronization.
func (i *ChanInput) Write(p []byte) (int, error) {
	i.ch <- p
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

// WriteTLV writes a TLV-encoded message to the input.
func (i *ChanInput) WriteTLV(tag string, value string) error {
	_, err := i.Write(EncodeTLV(tag, value))
	return err
}

// WriteOutputTLV writes a TLV message to the output.
//
// IMPORTANT: Implementations of io.Writer passed to this function MUST be
// safe for concurrent use. The session calls WriteOutputTLV from two
// goroutines — taskRunner and readFromInput — so the underlying writer
// needs a mutex or equivalent synchronization internally.
func WriteOutputTLV(output io.Writer, tag string, value string) error {
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

// NopOutput is a Writer that discards all output.
type NopOutput struct{}

func (n *NopOutput) Write(p []byte) (int, error) {
	return len(p), nil
}

// ToolCallData represents a tool call (FC tag payload).
type ToolCallData struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input string `json:"input"`
}

// ToolResultData represents a tool result (FR tag payload).
type ToolResultData struct {
	ID     string `json:"id"`
	Output string `json:"output"`
}

// ToolStateData represents a tool state indicator (FS tag payload).
type ToolStateData struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// ReasoningData represents reasoning/thinking content with optional signature.
// Used for persistence when Anthropic's thinking block includes a signature.
// Text is the thinking content; Signature is Anthropic-specific and only
// present on thinking blocks.
type ReasoningData struct {
	Text      string `json:"text"`
	Signature string `json:"signature,omitempty"`
}
