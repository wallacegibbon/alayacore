// Package stream provides the minimal IO abstraction and TLV encoding
// used between adaptors (terminal/plainio) and the core session.
//
// The stream package defines a simple Input/Output pair plus helpers
// for reading/writing framed Tag-Length-Value (TLV) messages.
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
//	  - TagFunctionState (FS): Function state indicator (pending/success/error)
//	  - TagSystemError (SE): System error messages
//	  - TagSystemNotify (SN): System notifications
//	  - TagSystemData (SD): System data (JSON)
//
// State Indicators:
//
// The TagFunctionState tag is used to display state indicators for tool calls:
//   - "pending": Tool is currently executing (• dimmed filled dot)
//   - "success": Tool executed successfully (• green filled dot)
//   - "error": Tool execution failed (• red filled dot)
//   - default: Tool from loaded session with no status (· dimmed hollow dot)
//
// Delta Messages:
//
// TA, TR, and FS are delta messages that arrive piece-by-piece during
// streaming. Their TLV values use NUL-delimited stream IDs:
//
//	\x00<stream-id>\x00<content>
//
// NUL bytes (\x00) are used as delimiters because they can never appear in
// normal UTF-8 text, making the split unambiguous. See WrapDelta and
// UnwrapDelta in stream_id.go.
//
// Stream ID formats differ by tag:
//   - TA, TR: "<promptID>-<step>-<suffix>" where suffix is "t" or "r"
//   - FS: free-form tool call ID assigned by the LLM provider
//
// Key Types:
//
//   - ChanInput: Input implementation using a channel of TLV messages
//   - Input: Interface for reading bytes
//   - Output: Interface for writing bytes with Flush
//
// Usage:
//
//	// Create input channel
//	input := stream.NewChanInput(10)
//
//	// Emit a TLV message
//	input.EmitTLV(stream.TagTextUser, "Hello, AI!")
//
//	// Read TLV from session
//	tag, value, err := stream.ReadTLV(input)
//
//	// Write TLV to output
//	stream.WriteTLV(output, stream.TagTextAssistant, "Hello, human!")
package stream
