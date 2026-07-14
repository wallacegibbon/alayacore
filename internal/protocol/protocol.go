// Package protocol defines the high-level message types and wire format
// for the AlayaCore communication protocol.
//
// It provides the SystemMsg interface and implementations (ErrorMsg, NotifyMsg,
// ToolConfirmMsg), the tool data structures (ToolInputData, ToolOutputData), and
// helpers for reading/writing system messages as TLV frames.
//
// The protocol layer depends on the internal/tlv package for low-level frame
// encoding.
package protocol

import (
	"encoding/json"
	"io"

	"github.com/alayacore/alayacore/internal/tlv"
)

// SystemMsgType constants identify the type field of a TagSystemMsg frame.
// These are used by adapters to dispatch system messages without string literals.
type SystemMsgType string

const (
	MsgTypeError       SystemMsgType = "error"
	MsgTypeNotify      SystemMsgType = "notify"
	MsgTypeTask        SystemMsgType = "task"
	MsgTypeModel       SystemMsgType = "model"
	MsgTypeModelList   SystemMsgType = "model_list"
	MsgTypeTheme       SystemMsgType = "theme"
	MsgTypeThemeList   SystemMsgType = "theme_list"
	MsgTypeReasoning   SystemMsgType = "reasoning"
	MsgTypeVideoConfig SystemMsgType = "video_config"
	MsgTypeToolConfirm SystemMsgType = "tool_confirm"
	MsgTypeMCP         SystemMsgType = "mcp"
	MsgTypeVersion     SystemMsgType = "version"
)

// SystemMsg is implemented by all TagSystemMsg payloads.
type SystemMsg interface {
	SystemMsgType() string
}

// SystemMsgEnvelope is the wire format for TagSystemMsg.
type SystemMsgEnvelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// ParseSystemMsg parses a TagSystemMsg value into its type and payload.
// Returns the envelope on success, or an error if the JSON is malformed.
func ParseSystemMsg(value string) (SystemMsgEnvelope, error) {
	var env SystemMsgEnvelope
	if err := json.Unmarshal([]byte(value), &env); err != nil {
		return SystemMsgEnvelope{}, err
	}
	return env, nil
}

// ParseSystemMsgType extracts just the type from a TagSystemMsg value.
// Returns the type string and whether parsing succeeded.
func ParseSystemMsgType(value string) (string, bool) {
	env, err := ParseSystemMsg(value)
	return env.Type, err == nil
}

// WriteSystemMsg marshals msg as a TagSystemMsg TLV frame.
//
// It builds the envelope JSON manually instead of using a second json.Marshal
// call. msg.SystemMsgType() returns safe ASCII constants (defined in this
// package and in agent/session_types.go), so no JSON escaping is needed for
// the type field.
func WriteSystemMsg(w io.Writer, msg SystemMsg) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	// Build {"type":"<type>","data":<data>} as a single string.
	s := `{"type":"` + msg.SystemMsgType() + `","data":` + string(data) + `}`
	return tlv.WriteTLV(w, tlv.TagSystemMsg, s)
}

// ToolInputData is the JSON payload for TagAssistantF (AF).
// A frame with a non-empty Name and empty Input is a preliminary
// "start" frame that announces the tool name. All other frames
// carry the actual tool arguments.
type ToolInputData struct {
	ID    string          `json:"id"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// ToolInputDeltaData is the JSON payload for TagAssistantFDelta (Af).
// Carries a partial JSON chunk of tool arguments during streaming.
type ToolInputDeltaData struct {
	ID    string `json:"id"`
	Delta string `json:"delta"`
}

// ToolOutputData is the JSON payload for TagUserF (UF).
// Output is a JSON array of content blocks (text, image, etc.).
// IsError indicates whether the tool completed with an error.
type ToolOutputData struct {
	ID      string          `json:"id"`
	Output  json.RawMessage `json:"output"`
	IsError bool            `json:"is_error,omitempty"`
}

// ErrorMsg is a system error message (type "error").
type ErrorMsg struct {
	Text string `json:"text"`
}

func (ErrorMsg) SystemMsgType() string { return "error" }

// NotifyMsg is a system notification (type "notify").
type NotifyMsg struct {
	Text string `json:"text"`
}

func (NotifyMsg) SystemMsgType() string { return "notify" }

// ToolConfirmMsg is sent when a tool call needs user confirmation
// (type "tool_confirm").
//
// Request (agent -> adapter):
//
//	SM {"type":"tool_confirm","data":{"id":"<toolUseID>"}}
//
// Response (adapter -> agent):
//
//	SM {"type":"tool_confirm","data":{"id":"<toolUseID>","allowed":true|false}}
type ToolConfirmMsg struct {
	ID      string `json:"id"`
	Allowed *bool  `json:"allowed,omitempty"`
}

func (ToolConfirmMsg) SystemMsgType() string { return "tool_confirm" }
