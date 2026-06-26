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
//	  - TagUserI (UI): User image (data:image/...;base64,... or URL)
//	  - TagUserV (UV): User video (data:video/...;base64,... or URL)
//	  - TagUserA (UA): User audio (data:audio/...;base64,... or URL)
//	  - TagUserD (UD): User document (data:application/...;base64,... or URL)
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
// All content tags (UT, UI, UV, UA, UD, AT, AR, AF, UF) may carry a NUL-delimited
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
// normal UTF-8 text, making the split unambiguous. See WrapID and
// UnwrapID in stream.go.
//
// The id is a flat monotonic counter from the session's history counter:
//   - User content (UT, UI, cancel AT): incremented on echo
//   - Delta content (AT, AR, AF): incremented per content block
//   - Tool results (UF): incremented on result
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
