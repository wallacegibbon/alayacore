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
	// Provider succeeds for step 1 (with tool call, so loop continues),
	// then fails on step 2. This gives doAutoSummarize a non-nil
	// fullContents from OnStepFinish when it hits the error path.
	provider := &mockProviderStepFail{
		responses: []stepResponse{
			{text: "partial summary", toolCalls: []llm.ToolInputPart{{ID: "s1", Name: "t", Input: []byte(`{}`)}}},
			{failErr: errors.New("provider failed on step 2")},
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

	// Must contain the step 1 content (partial summary).
	hasPartialSummary := false
	for _, p := range result {
		if tp, ok := p.(*llm.TextPart); ok && tp.Role == llm.RoleAssistant && tp.Text == "partial summary" {
			hasPartialSummary = true
		}
	}
	if !hasPartialSummary {
		t.Fatal("CRITICAL BUG: doAutoSummarize lost step 1 content on error — " +
			"if len(fullContents) > 0 check may have been removed")
	}
}
