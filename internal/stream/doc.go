// Package stream provides the minimal IO abstraction and TLV encoding
// used between adaptors (terminal/plainio/rawio) and the core session.
//
// The stream package provides helpers for reading/writing framed
// Tag-Length-Value (TLV) messages over io.Reader and io.Writer.
//
// The core type SliceReadWriter bridges slice-oriented writes
// (each Write call sends an atomic slice) with byte-oriented reads
// (Read buffers slices into a continuous byte stream).
//
// TLV Protocol:
//
//	Messages are encoded as:
//	  [2-byte tag][4-byte length (big-endian)][value bytes]
//
//	Tag values are 2-character strings:
//	  - TagTextUser (TU): User text input
//	  - TagTextAssistant (TA): Assistant text output
//	  - TagTextReasoning (TR): Reasoning/thinking content
//	  - TagFunctionCall (FC): Function call (JSON: id, name, input)
//	  - TagFunctionResult (FR): Function result (JSON: id, output)
//	  - TagFunctionState (FS): Function state indicator (JSON: id, status)
//	  - TagSystemError (SE): System error messages
//	  - TagSystemNotify (SN): System notifications
//	  - TagSystemData (SD): System data (JSON)
//
// State Indicators:
//
// The TagFunctionState tag carries a JSON payload with the tool call ID and
// status, used by the terminal to display live status indicators:
//   - `{"id":"tool123","status":"pending"}` — Tool is currently executing
//   - `{"id":"tool123","status":"success"}` — Tool executed successfully
//   - `{"id":"tool123","status":"error"}` — Tool execution failed
//
// Delta Messages:
//
// TA and TR are delta messages that arrive piece-by-piece during streaming.
// Their TLV values use NUL-delimited stream IDs:
//
//	\x00<stream-id>\x00<content>
//
// NUL bytes (\x00) are used as delimiters because they can never appear in
// normal UTF-8 text, making the split unambiguous. See WrapDelta and
// UnwrapDelta in stream_id.go.
//
// Stream ID format for TA/TR:
//
//	"<promptID>|<step>"
//
// The adaptor disambiguates text vs reasoning by using tag+id as the window key.
//
// Usage:
//
//	// Create input channel
//	input := stream.NewSliceReadWriter(10)
//
//	// Emit a TLV message
//	stream.WriteTLV(input, stream.TagTextUser, "Hello, AI!")
//
//	// Read TLV from input (io.Reader)
//	tag, value, err := stream.ReadTLV(input)
//
//	// Write TLV to output
//	stream.WriteTLV(output, stream.TagTextAssistant, "Hello, human!")
package stream
