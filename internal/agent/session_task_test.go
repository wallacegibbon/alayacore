package agent

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"sync/atomic"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

type stepResponse struct {
	text      string
	toolCalls []llm.ToolInputPart
	failErr   error
}

type mockProviderStepFail struct {
	responses []stepResponse
	callCount int
}

func (m *mockProviderStepFail) StreamMessages(_ context.Context, _ []llm.ContentPart, _ []llm.ToolDefinition, _, _ string) (iter.Seq2[llm.StreamEvent, error], error) {
	if m.callCount >= len(m.responses) {
		return nil, errors.New("mock: unexpected call beyond configured responses")
	}
	resp := m.responses[m.callCount]
	m.callCount++
	if resp.failErr != nil {
		return nil, resp.failErr
	}
	return func(yield func(llm.StreamEvent, error) bool) {
		if resp.text != "" {
			if !yield(llm.TextDeltaEvent{Delta: resp.text, Index: 0}, nil) {
				return
			}
		}
		for _, tc := range resp.toolCalls {
			if !yield(llm.ToolInputStartEvent{ID: tc.ID, Name: tc.Name, Index: 0}, nil) {
				return
			}
			if !yield(llm.ToolInputCompleteEvent{ID: tc.ID, Input: tc.Input, Index: 0}, nil) {
				return
			}
		}
		content := []llm.ContentPart{}
		if resp.text != "" {
			content = append(content, &llm.TextPart{
				Text:            resp.text,
				ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant},
			})
		}
		for _, tc := range resp.toolCalls {
			content = append(content, &llm.ToolInputPart{
				ID:              tc.ID,
				Name:            tc.Name,
				Input:           tc.Input,
				ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant},
			})
		}
		yield(llm.StepCompleteEvent{
			Contents: content,
			Usage:    llm.Usage{InputTokens: 10, OutputTokens: 20},
		}, nil)
	}, nil
}

func (m *mockProviderStepFail) SetReasoningLevel(_ int)     {}
func (m *mockProviderStepFail) SetVideoConfig(_ int, _ int) {}

func TestHandleUserPromptPreservesPartialResultsOnError(t *testing.T) {
	provider := &mockProviderStepFail{
		responses: []stepResponse{
			{text: "Step 1.", toolCalls: []llm.ToolInputPart{{ID: "c1", Name: "t", Input: []byte(`{}`)}}},
			{text: "Step 2.", toolCalls: []llm.ToolInputPart{{ID: "c2", Name: "t", Input: []byte(`{}`)}}},
			{failErr: errors.New("provider fail on step 3")},
		},
	}
	agent := llm.NewAgent(llm.AgentConfig{
		Provider: provider,
		Tools: []llm.Tool{{
			Definition: llm.ToolDefinition{Name: "t", Description: "test", Schema: []byte(`{"type":"object"}`)},
			Execute:    func(_ context.Context, _ json.RawMessage) ([]llm.ContentPart, error) { return nil, nil },
		}},
		MaxSteps: 10,
	})
	session := &Session{
		sessionConfig: sessionConfig{
			modelService:  &ModelService{agent: agent},
			SessionConfig: SessionConfig{NoDelta: true},
		},
		sharedState: sharedState{
			histCounter:  200,
			outputBroken: atomic.Bool{},
		},
		runState: runState{
			taskEventCh: make(chan TaskEvent, 20),
		},
	}
	prev := []llm.ContentPart{
		&llm.TextPart{Text: "hi", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
		&llm.TextPart{Text: "hello", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
	}
	result, _ := session.handleUserPrompt(context.Background(), prev, []llm.ContentPart{
		&llm.TextPart{Text: "do it"},
	})
	assistantCount := 0
	toolCallCount := 0
	for _, p := range result {
		if _, ok := p.(*llm.ToolInputPart); ok {
			toolCallCount++
		}
		if p.GetRole() == llm.RoleAssistant {
			assistantCount++
		}
	}
	if assistantCount < 3 {
		t.Fatalf("expected >=3 assistant parts (prev + 2 steps), got %d", assistantCount)
	}
	if toolCallCount != 2 {
		t.Fatalf("expected 2 tool calls, got %d", toolCallCount)
	}
}

func TestDoAutoSummarizePreservesContentsOnError(t *testing.T) {
	// Summarization failed immediately (no successful step).
	provider := &mockProviderStepFail{
		responses: []stepResponse{
			{failErr: errors.New("provider failed on summarization")},
		},
	}
	agent := llm.NewAgent(llm.AgentConfig{
		Provider: provider,
		MaxSteps: 1,
	})
	session := &Session{
		sessionConfig: sessionConfig{
			modelService:  &ModelService{agent: agent},
			SessionConfig: SessionConfig{NoDelta: true},
		},
		sharedState: sharedState{
			histCounter:  300,
			outputBroken: atomic.Bool{},
		},
		runState: runState{
			taskEventCh: make(chan TaskEvent, 20),
		},
	}
	contents := []llm.ContentPart{
		&llm.TextPart{Text: "existing content", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	}
	result := session.doAutoSummarize(context.Background(), contents)

	if len(result) == 0 {
		t.Fatal("doAutoSummarize returned empty on error")
	}
	if tp, ok := result[0].(*llm.TextPart); !ok || tp.Text != "existing content" {
		t.Errorf("expected original content preserved, got %v", result[0])
	}
}

func TestDoAutoSummarizeBuildSummaryFails(t *testing.T) {
	// processPrompt succeeds (step 1 tool calls → loop continues,
	// step 2 empty → no tool calls → loop exits with success).
	// The response has no text, so buildSummary returns an error.
	provider := &mockProviderStepFail{
		responses: []stepResponse{
			{toolCalls: []llm.ToolInputPart{{ID: "s1", Name: "t", Input: []byte(`{}`)}}},
			{}, // empty step — no text, no tool calls
		},
	}
	agent := llm.NewAgent(llm.AgentConfig{
		Provider: provider,
		Tools: []llm.Tool{{
			Definition: llm.ToolDefinition{Name: "t", Description: "test", Schema: []byte(`{"type":"object"}`)},
			Execute:    func(_ context.Context, _ json.RawMessage) ([]llm.ContentPart, error) { return nil, nil },
		}},
		MaxSteps: 5,
	})
	session := &Session{
		sessionConfig: sessionConfig{
			modelService:  &ModelService{agent: agent},
			SessionConfig: SessionConfig{NoDelta: true},
		},
		sharedState: sharedState{
			histCounter:  400,
			outputBroken: atomic.Bool{},
		},
		runState: runState{
			taskEventCh: make(chan TaskEvent, 20),
		},
	}
	contents := []llm.ContentPart{
		&llm.TextPart{Text: "original", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	}
	result := session.doAutoSummarize(context.Background(), contents)

	// buildSummary failed (no text) — original history must be preserved.
	if len(result) != 1 {
		t.Fatalf("expected 1 part (original history), got %d", len(result))
	}
	if tp, ok := result[0].(*llm.TextPart); !ok || tp.Text != "original" {
		t.Errorf("expected original content preserved, got %v", result[0])
	}
}

// ============================================================================
// Unit tests for buildSummary
// ============================================================================

func TestBuildSummaryWithText(t *testing.T) {
	session := &Session{
		sharedState: sharedState{histCounter: 100},
		runState:    runState{taskEventCh: make(chan TaskEvent, 10)},
	}

	// Simulate: [original history] + [summarize prompt] + [assistant summary text]
	fullContents := []llm.ContentPart{
		&llm.TextPart{Text: "user msg", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
		&llm.TextPart{Text: "assistant msg", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
		&llm.TextPart{Text: summarizePrompt, ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
		&llm.TextPart{Text: "This is the summary.", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
	}
	beforeLen := 2

	result, err := session.buildSummary(fullContents[beforeLen:], 50)
	if err != nil {
		t.Fatalf("buildSummary failed: %v", err)
	}

	// Result should be: "Continue" (user) + "This is the summary." (assistant)
	if len(result) != 2 {
		t.Fatalf("expected 2 parts (Continue + summary), got %d", len(result))
	}
	if tp, ok := result[0].(*llm.TextPart); !ok || tp.Text != "Continue" || tp.Role != llm.RoleUser {
		t.Errorf("first part should be Continue user message, got %T %+v", result[0], result[0])
	}
	if tp, ok := result[1].(*llm.TextPart); !ok || tp.Text != "This is the summary." || tp.Role != llm.RoleAssistant {
		t.Errorf("second part should be summary text, got %T %+v", result[1], result[1])
	}
}

func TestBuildSummaryNoText(t *testing.T) {
	session := &Session{
		sharedState: sharedState{histCounter: 200},
		runState:    runState{taskEventCh: make(chan TaskEvent, 10)},
	}

	// Only tool calls, no TextPart — should return original history unchanged.
	// Original content: 2 parts before beforeLen
	original := []llm.ContentPart{
		&llm.TextPart{Text: "user msg", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
		&llm.TextPart{Text: "assistant msg", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
	}
	fullContents := make([]llm.ContentPart, len(original)+2)
	copy(fullContents, original)
	fullContents[len(original)] = &llm.TextPart{Text: summarizePrompt, ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}}
	fullContents[len(original)+1] = &llm.ToolInputPart{ID: "c1", Name: "t", Input: []byte(`{}`), ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}}
	beforeLen := 2

	_, err := session.buildSummary(fullContents[beforeLen:], 0)
	if err == nil {
		t.Fatal("buildSummary should return error when last part is not text")
	}
}

func TestBuildSummaryLastPartNotText(t *testing.T) {
	session := &Session{
		sharedState: sharedState{histCounter: 300},
		runState:    runState{taskEventCh: make(chan TaskEvent, 10)},
	}

	// Last part is a tool call, not text — summarization failed,
	// even though there's text earlier in the response.
	original := []llm.ContentPart{
		&llm.TextPart{Text: "user msg", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
		&llm.TextPart{Text: "assistant msg", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}},
	}
	fullContents := make([]llm.ContentPart, len(original)+4)
	copy(fullContents, original)
	fullContents[len(original)] = &llm.TextPart{Text: summarizePrompt, ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}}
	fullContents[len(original)+1] = &llm.TextPart{Text: "Some summary.", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}}
	fullContents[len(original)+2] = &llm.ToolInputPart{ID: "c1", Name: "t", Input: []byte(`{}`), ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}}
	fullContents[len(original)+3] = &llm.ToolInputPart{ID: "c2", Name: "t", Input: []byte(`{}`), ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}}
	beforeLen := 2

	_, err := session.buildSummary(fullContents[beforeLen:], 0)
	if err == nil {
		t.Fatal("buildSummary should return error when last part is tool call")
	}
}

func TestBuildSummaryEmptyText(t *testing.T) {
	session := &Session{
		sharedState: sharedState{histCounter: 400},
		runState:    runState{taskEventCh: make(chan TaskEvent, 10)},
	}

	// TextPart with empty text — should be treated as no text.
	original := []llm.ContentPart{
		&llm.TextPart{Text: "existing", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}},
	}
	fullContents := make([]llm.ContentPart, len(original)+2)
	copy(fullContents, original)
	fullContents[len(original)] = &llm.TextPart{Text: summarizePrompt, ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleUser}}
	fullContents[len(original)+1] = &llm.TextPart{Text: "", ContentPartMeta: llm.ContentPartMeta{Role: llm.RoleAssistant}}
	beforeLen := 1

	_, err := session.buildSummary(fullContents[beforeLen:], 0)
	if err == nil {
		t.Fatal("buildSummary should return error when last text is empty")
	}
}

func TestBuildSummaryEmptyResponse(t *testing.T) {
	session := &Session{
		sharedState: sharedState{histCounter: 500},
		runState:    runState{taskEventCh: make(chan TaskEvent, 10)},
	}

	_, err := session.buildSummary(nil, 0)
	if err == nil {
		t.Fatal("buildSummary should return error on nil response")
	}

	_, err = session.buildSummary([]llm.ContentPart{}, 0)
	if err == nil {
		t.Fatal("buildSummary should return error on empty response")
	}
}
