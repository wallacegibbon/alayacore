package agent

// ContentPart ↔ TLV serialization and shared marshal helpers.
//
// This file is the single source of truth for serializing ContentParts
// to/from TLV tag+value pairs. All TLV serialization (persistence,
// streaming output, session replay) must go through these functions
// to avoid duplicating the switch dispatch logic.

import (
	"encoding/json"
	"fmt"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/stream"
)

// ============================================================================
// Shared Marshal Helpers
// ============================================================================

// marshalToolInputData marshals a tool call (ToolInputData) to JSON bytes.
// Each field is included only when non-zero, matching the TLV protocol's
// start-vs-complete frame distinction:
//   - Start frame: id + name (input is nil/empty)
//   - Complete frame: id + input (name is empty)
//   - Full (persistence): id + name + input
func marshalToolInputData(id, name string, input json.RawMessage) ([]byte, error) {
	data, err := json.Marshal(stream.ToolInputData{
		ID:    id,
		Name:  name,
		Input: input,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tool input data: %w", err)
	}
	return data, nil
}

// marshalToolOutputData marshals a tool result (ToolOutputData) to JSON bytes.
// On serialization error for the inner content parts, a fallback text is used
// so the caller never receives a marshal error from content serialization —
// only from the outer JSON marshal.
func marshalToolOutputData(id string, contents []llm.ContentPart, isError bool) ([]byte, error) {
	contentJSON, serErr := serializeContentParts(contents)
	if serErr != nil {
		contentJSON = []byte(`[{"type":"text","text":"(serialization error)"}]`)
	}
	data, err := json.Marshal(stream.ToolOutputData{
		ID:      id,
		Output:  contentJSON,
		IsError: isError,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tool output data: %w", err)
	}
	return data, nil
}

// ============================================================================
// ContentPart ↔ TLV
// ============================================================================

// contentPartToTLV serializes a ContentPart as a TLV tag and value string (without history ID).
func contentPartToTLV(part llm.ContentPart) (tag string, content string, err error) {
	switch p := part.(type) {
	case *llm.TextPart:
		if part.GetRole() == llm.RoleAssistant {
			return stream.TagAssistantT, p.Text, nil
		}
		return stream.TagUserT, p.Text, nil
	case *llm.ImagePart:
		return stream.TagUserI, p.URI, nil
	case *llm.VideoPart:
		return stream.TagUserV, p.URI, nil
	case *llm.AudioPart:
		return stream.TagUserA, p.URI, nil
	case *llm.DocumentPart:
		return stream.TagUserD, p.URI, nil
	case *llm.ReasoningPart:
		return stream.TagAssistantR, p.Text, nil
	case *llm.ToolInputPart:
		jsonData, err := marshalToolInputData(p.ID, p.Name, p.Input)
		if err != nil {
			return "", "", err
		}
		return stream.TagAssistantF, string(jsonData), nil
	case *llm.ToolOutputPart:
		jsonData, err := marshalToolOutputData(p.ID, p.Output, p.IsError)
		if err != nil {
			return "", "", err
		}
		return stream.TagUserF, string(jsonData), nil
	default:
		return "", "", fmt.Errorf("unknown content part type: %T", part)
	}
}

// contentPartFromTLV converts a TLV record into a ContentPart with Role set.
func contentPartFromTLV(tag string, content []byte) (llm.ContentPart, error) {
	cleanContent := string(content)
	if _, stripped, ok := stream.UnwrapID(cleanContent); ok {
		cleanContent = stripped
	}

	switch tag {
	case stream.TagUserT:
		return &llm.TextPart{Text: cleanContent, ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}}, nil
	case stream.TagUserI:
		return &llm.ImagePart{URI: cleanContent, ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}}, nil
	case stream.TagUserV:
		return &llm.VideoPart{URI: cleanContent, ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}}, nil
	case stream.TagUserA:
		return &llm.AudioPart{URI: cleanContent, ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}}, nil
	case stream.TagUserD:
		return &llm.DocumentPart{URI: cleanContent, ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}}, nil
	case stream.TagAssistantT:
		return &llm.TextPart{Text: cleanContent, ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}}, nil
	case stream.TagAssistantR:
		return &llm.ReasoningPart{Text: cleanContent, ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}}, nil
	case stream.TagAssistantF:
		var fd stream.ToolInputData
		if err := json.Unmarshal([]byte(cleanContent), &fd); err != nil {
			return nil, fmt.Errorf("failed to parse function data: %w", err)
		}
		if fd.Name == "" {
			return nil, nil
		}
		return &llm.ToolInputPart{
			ID: fd.ID, Name: fd.Name, Input: fd.Input, ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant},
		}, nil
	case stream.TagUserF:
		var tr stream.ToolOutputData
		if err := json.Unmarshal([]byte(cleanContent), &tr); err != nil {
			return nil, fmt.Errorf("failed to parse tool result: %w", err)
		}
		contentParts, err := deserializeContentParts(tr.Output)
		if err != nil {
			return nil, fmt.Errorf("failed to parse tool result content: %w", err)
		}
		return &llm.ToolOutputPart{ID: tr.ID, Output: contentParts, IsError: tr.IsError, ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleTool}}, nil
	default:
		return nil, fmt.Errorf("unknown tag: %s", tag)
	}
}
