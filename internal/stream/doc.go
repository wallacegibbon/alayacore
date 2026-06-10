// Package stream provides the minimal IO abstraction and TLV encoding
// used between adapters (terminal/plainio/rawio) and the core session.
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
//	  - TagUserT (UT): User text input
//	  - TagUserI (UI): User image (DataURI: data:image/...;base64,...)
//	  - TagAssistantT (AT): Assistant text output
//	  - TagAssistantR (AR): Reasoning/thinking content
//	  - TagAssistantF (AF): Function lifecycle (JSON: id, name, input)
//	  - TagUserF (UF): Function result (JSON: id, output, is_error)
//	  - TagSystemMsg (SM): System message (JSON: {"type":"...","data":{...}})
//
// Function Lifecycle:
//
// The TagAssistantF tag (AF) carries a JSON payload:
//   - `{"id":"tool123","name":"read_file"}` — tool name known (start frame)
//   - `{"id":"tool123","input":{...}}` — full tool arguments (input frame)
//
// TagUserF (UF) carries the final output and error flag:
//   - `{"id":"tool123","output":[{"type":"text","text":"..."}]}` — execution succeeded (`is_error` omitted when false)
//   - `{"id":"tool123","output":[{"type":"text","text":"..."}],"is_error":true}` — execution failed
//
// Output is a JSON array of content blocks (text, image, etc.) matching the
// format used by Anthropic's tool_result content blocks. Single text results
// are rendered as `[{"type":"text","text":"..."}]`.
//
// The terminal infers "pending" while waiting for a result (no UF received).
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
//	"<promptID>|<step>|<index>"
//
// The index uniquely identifies each content block within a step:
//   - Anthropic: uses the content block index from the API.
//     Blocks can interleave (e.g. thinking, text, thinking, text, tool_use),
//     each with a unique sequential index regardless of type. This lets the
//     adapter route deltas to the correct window — without it, two reasoning
//     blocks in the same step would be indistinguishable.
//   - OpenAI: reasoning blocks get index 0, text blocks get index 1.
//     OpenAI never emits multiple blocks of the same type, so fixed values
//     are sufficient.
//
// The stream ID itself serves as the window key — the index ensures each
// content block has a unique ID regardless of type, so no tag prefix is
// needed for disambiguation.
//
// Usage:
//
//	// Create input channel
//	input := stream.NewSliceBuffer(10)
//
//	// Emit a TLV message
//	stream.WriteTLV(input, stream.TagUserT, "Hello, AI!")
//
//	// Read TLV from input (io.Reader)
//	tag, value, err := stream.ReadTLV(input)
//
//	// Write TLV to output
//	stream.WriteTLV(output, stream.TagAssistantT, "Hello, human!")
package stream
