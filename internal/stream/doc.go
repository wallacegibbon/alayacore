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
//	  - TagUserV (UV): User video (DataURI: data:video/...;base64,...)
//	  - TagUserA (UA): User audio (DataURI: data:audio/...;base64,...)
//	  - TagUserD (UD): User document (DataURI: data:application/...;base64,...)
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
// All content tags (UT, UI, AT, AR, AF, UF) may carry a NUL-delimited
// historyCount-based ID for live streaming. The adapter uses this ID to
// route content to the correct window. When messages are replayed from a
// saved session file the ID is absent and the adapter falls back to
// generating a local window ID.
//
// TLV value format with ID:
//
//	\x00<id>\x00<content>
//
// NUL bytes (\x00) are used as delimiters because they can never appear in
// normal UTF-8 text, making the split unambiguous. See WrapDelta and
// UnwrapDelta in stream_id.go.
//
// The id is derived from historyCount + blockIndex:
//   - User content (UT, UI, cancel AT): historyCount (incremented on echo)
//   - Delta content (AT, AR, AF): historyCount + index
//   - Tool results (UF): historyCount (incremented on result)
//
// The blockIndex uniquely identifies each content block within a step.
//   - Anthropic: uses the content block index from the API.
//     Blocks can interleave (e.g. thinking, text, thinking, text, tool_use),
//     each with a unique sequential index regardless of type. This lets the
//     adapter route deltas to the correct window — without it, two reasoning
//     blocks in the same step would be indistinguishable.
//   - OpenAI: reasoning blocks get index 0, text blocks get index 1,
//     and tool calls get index 2+. OpenAI never emits multiple blocks of
//     the same type, so fixed values are sufficient.
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
