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
//	  - TagUserT (UT): User text — sent to agent on stdin; echoed by agent on stdout
//	  - TagUserI (UI): User image — sent to agent on stdin; echoed by agent on stdout
//	  - TagUserV (UV): User video — sent to agent on stdin; echoed by agent on stdout
//	  - TagUserA (UA): User audio — sent to agent on stdin; echoed by agent on stdout
//	  - TagUserD (UD): User document — sent to agent on stdin; echoed by agent on stdout
//	  - TagUserEnd (UE): User message end — marks the end of a user message on stdin
//	  - TagAssistantT (AT): Assistant text output (stdout)
//	  - TagAssistantR (AR): Reasoning/thinking content (stdout)
//	  - TagAssistantF (AF): Function lifecycle — JSON: id, name, input (stdout)
//	  - TagUserF (UF): Function result — JSON: id, output, is_error (stdout)
//	  - TagSystemMsg (SM): System message — JSON: {"type":"...","data":{...}} (stdout)
//
// User tags (UT, UI, UV, UA, UD) are bidirectional:
//   - stdin: adapter sends new user input to the agent
//   - stdout: agent echoes the user's message back with an assigned history ID
//     Adapters must handle user tags on both stdin AND stdout.
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
