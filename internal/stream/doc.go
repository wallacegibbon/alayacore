// Package stream provides the minimal IO abstraction and TLV encoding
// used between adaptors (terminal/plainio/rawio) and the core session.
//
// The stream package provides helpers for reading/writing framed
// Tag-Length-Value (TLV) messages over io.Reader and io.Writer.
//
// The core type SliceBuffer bridges slice-oriented writes
// (each Write call sends an atomic slice) with byte-oriented reads
// (Read buffers slices into a continuous byte stream).
//
// TLV Protocol:
//
//	Messages are encoded as:
//	  [2-byte tag][4-byte length (big-endian)][value bytes]
//
//	Tag values are 2-character strings:
//	  - TagTextUser (UT): User text input
//	  - TagTextAssistant (AT): Assistant text output
//	  - TagTextReasoning (AR): Reasoning/thinking content
//	  - TagFunction (AF): Function lifecycle (JSON: id, type, name, input)
//	  - TagFunctionResult (UF): Function result (JSON: id, output, status)
//	  - TagSystemError (SE): System error messages
//	  - TagSystemNotify (SN): System notifications
//	  - TagSystemData (SD): System data (JSON)
//
// Function Lifecycle:
//
// The TagFunction tag (AF) carries a JSON payload with a type discriminator:
//   - `{"id":"tool123","type":"start","name":"read_file"}` — tool name known
//   - `{"id":"tool123","type":"call","name":"read_file","input":"..."}` — full input
//
// TagFunctionResult (UF) carries the final output and status:
//   - `{"id":"tool123","output":"...","status":"success"}` — execution succeeded
//   - `{"id":"tool123","output":"...","status":"failed"}` — execution failed
//
// The terminal infers "pending" while waiting for a result (no UF received).
// The AF "state" type was removed — the final status arrives via UF.
//
// Delta Messages:
//
// AT and AR are delta messages that arrive piece-by-piece during streaming.
// Their TLV values use NUL-delimited stream IDs:
//
//	\x00<stream-id>\x00<content>
//
// NUL bytes (\x00) are used as delimiters because they can never appear in
// normal UTF-8 text, making the split unambiguous. See WrapDelta and
// UnwrapDelta in stream_id.go.
//
// Stream ID format for AT/AR:
//
//	"<promptID>|<step>"
//
// The adaptor disambiguates text vs reasoning by using tag+id as the window key.
//
// Usage:
//
//	// Create input channel
//	input := stream.NewSliceBuffer(10)
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
