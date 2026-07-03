// Package tlv provides TLV (Tag-Length-Value) frame encoding and decoding
// for the AlayaCore communication protocol.
//
// Wire format: [2-byte tag][4-byte big-endian length][value bytes]
//
// Tag values are 2-character strings identifying the content type:
//   - UT: User text
//   - UI: User image
//   - UV: User video
//   - UA: User audio
//   - UD: User document
//   - UE: User message end
//   - AT: Assistant text
//   - AR: Assistant reasoning
//   - AF: Assistant function (tool call)
//   - UF: User function (tool result)
//   - SM: System message
package tlv

import (
	"encoding/binary"
	"io"
	"strings"
)

// TLV tag constants - these are sent over the wire.
const (
	TagAssistantR = "AR" // Reasoning/thinking content
	TagAssistantT = "AT" // Assistant text output
	TagAssistantF = "AF" // JSON: id, type, name, input, status (function arguments)
	TagUserT      = "UT" // User text input
	TagUserF      = "UF" // JSON: id, output, status (function result)
	TagUserI      = "UI" // User image — data:image/...;base64,... or URL
	TagUserV      = "UV" // User video — data:video/...;base64,... or URL
	TagUserA      = "UA" // User audio — data:audio/...;base64,... or URL
	TagUserD      = "UD" // User document — data:application/...;base64,... or URL

	TagUserEnd = "UE" // User message end — flushes staged content as one window

	TagSystemMsg = "SM" // System message JSON: {"type":"...","data":{...}}
)

const maxMessageSize = 1<<31 - 1 // Max int32 to fit in uint32

// EncodeTLV creates a TLV-encoded byte slice.
// Format: [2-byte tag][4-byte length][value]
func EncodeTLV(tag string, value string) []byte {
	length := len(value)
	if length > maxMessageSize {
		length = maxMessageSize
		value = value[:maxMessageSize]
	}

	msg := make([]byte, 6+length)
	msg[0] = tag[0]
	msg[1] = tag[1]
	binary.BigEndian.PutUint32(msg[2:], uint32(length)) //nolint:gosec // G115: length is bounded by maxMessageSize
	copy(msg[6:], value)

	return msg
}

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

// WrapID prepends a NUL-delimited history ID to content.
// Format: \x00<id>\x00<content>
func WrapID(id string, content string) string {
	return "\x00" + id + "\x00" + content
}

// UnwrapID extracts the NUL-delimited history ID prefix from a value.
// Returns (id, content, true) on success, ("", value, false) if the
// value has no NUL prefix (e.g. plain text from session replay).
func UnwrapID(value string) (id string, content string, ok bool) {
	if len(value) == 0 || value[0] != 0 {
		return "", value, false
	}

	endIdx := strings.IndexByte(value[1:], 0)
	if endIdx == -1 {
		return "", value, false
	}
	endIdx++

	id = value[1:endIdx]
	if id == "" {
		return "", value, false
	}

	return id, value[endIdx+1:], true
}
