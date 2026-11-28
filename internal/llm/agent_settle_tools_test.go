package llm

// Tests for the step-boundary settlement: a step settles every tool window it
// opened and left without a result.
//
// A tool window is opened by the first frame about a call and closed by the frame
// that answers it. A step can end — cut, canceled, panicking — with windows it
// opened still unanswered, and each one is a spinner turning on the adapter's
// screen for a tool that is not running. The settlement is the step's own
// obligation, not a per-path discipline: these tests drive the shapes that reach
// it.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// settledCall is one tool window the agent closed for a call it never answered.
type settledCall struct {
	id      string
	history uint64
	text    string
	isErr   bool
}

// settleRecorder collects the OnToolOutput frames the agent fires, so a test can
// tell a result from a settlement.
type settleRecorder struct {
	outputs []settledCall
}

func (r *settleRecorder) onToolOutput(id string, contents []ContentPart, err error, historyID uint64) error {
	text := ""
	for _, p := range contents {
		if tp, ok := p.(*TextPart); ok {
			text += tp.Text
		}
	}
	r.outputs = append(r.outputs, settledCall{id: id, history: historyID, text: text, isErr: err != nil})
	return nil
}

func (r *settleRecorder) only(t *testing.T) settledCall {
	t.Helper()
	if len(r.outputs) != 1 {
		t.Fatalf("tool outputs = %d (%v), want exactly 1", len(r.outputs), r.outputs)
	}
	return r.outputs[0]
}

// toolCallIDs lists the calls a record holds, in order.
func toolCallIDs(contents []ContentPart) []string {
	var ids []string
	for _, p := range contents {
		if tc, ok := p.(*ToolInputPart); ok {
			ids = append(ids, tc.ID)
		}
	}
	return ids
}

// A tool call whose arguments were cut off mid-stream never becomes a part, so no
// result can ever arrive for it — but the adapter already holds its window. The
// step settles it as it ends: the window shows ✗ with an explanation instead of
// spinning until the task happens to end.
func TestCutStreamSettlesTheHalfStreamedToolCall(t *testing.T) {
	fastRetries(t)
	p := &streamScriptProvider{script: []streamScript{{
		events: []StreamEvent{
			ToolInputStartEvent{ID: "call_1", Name: "write_file", Key: "tool:0", Position: 1},
			ToolInputDeltaEvent{ID: "call_1", Delta: `{"path":"/tmp/`, Key: "tool:0"},
		},
		err: errors.New("stream vanished mid-arguments"),
	}}}
	agent := NewAgent(AgentConfig{Provider: p, MaxSteps: 3})
	rec := &settleRecorder{}
	// The session numbers every block; the settlement has to answer the window
	// under the same number it was opened with.
	var nextID uint64
	var openedWith uint64
	result, err := agent.Stream(context.Background(), userText(), StreamCallbacks{
		OnRetry:      p.onRetry,
		OnBeforeSend: (&resumeCaller{}).onBeforeSend,
		OnToolInputStart: func(_ string, _ string, historyID uint64) error {
			openedWith = historyID
			return nil
		},
		OnToolInputDelta: func(string, string, uint64) error { return nil },
		OnToolOutput:     rec.onToolOutput,
		IDGen:            func() uint64 { nextID++; return nextID },
	})
	if err != nil {
		t.Fatalf("Stream error = %v, want the continuation to finish the task", err)
	}
	if p.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (the cut step, then its continuation)", p.calls)
	}

	got := rec.only(t)
	if got.id != "call_1" {
		t.Errorf("settled call = %q, want %q", got.id, "call_1")
	}
	if got.text != errToolCallIncomplete.Error() {
		t.Errorf("settled output = %q, want %q", got.text, errToolCallIncomplete.Error())
	}
	if !got.isErr {
		t.Error("the settlement frame is not an error frame; the window would show ✓")
	}
	if got.history == 0 || got.history != openedWith {
		t.Errorf("settled frame history ID = %d, want %d — the ID the window was opened with", got.history, openedWith)
	}
	// The settlement is display-only: a call with no arguments cannot be replayed,
	// so it must not enter the record the next request is built from.
	if ids := toolCallIDs(result.Contents); len(ids) != 0 {
		t.Errorf("record holds tool calls %v, want none (the call never completed)", ids)
	}
}

// Serial mode queues every call until the stream ends, so a step that dies first
// runs none of them — and each one is a window with a spinner that has nothing
// behind it. They are settled too, and the tool itself never runs.
func TestSerialStepSettlesTheCallsItNeverRan(t *testing.T) {
	fastRetries(t)
	var ran int
	p := &streamScriptProvider{script: []streamScript{{
		events: []StreamEvent{
			ToolInputStartEvent{ID: "call_1", Name: "write_file", Key: "tool:0", Position: 1},
			ToolInputDeltaEvent{ID: "call_1", Delta: `{"path":"/tmp/x"}`, Key: "tool:0"},
			ToolInputCompleteEvent{ID: "call_1", Key: "tool:0", Position: 1},
		},
		err: errors.New("stream vanished after the arguments"),
	}}}
	agent := NewAgent(AgentConfig{
		Provider:        p,
		MaxSteps:        3,
		SerialToolCalls: true,
		Tools: []Tool{{
			Definition: ToolDefinition{Name: "write_file"},
			Execute: func(context.Context, json.RawMessage) ([]ContentPart, error) {
				ran++
				return nil, nil
			},
		}},
	})
	rec := &settleRecorder{}

	if _, err := agent.Stream(context.Background(), userText(), StreamCallbacks{
		OnRetry:      p.onRetry,
		OnBeforeSend: (&resumeCaller{}).onBeforeSend,
		OnToolOutput: rec.onToolOutput,
	}); err != nil {
		t.Fatalf("Stream error = %v, want the continuation to finish the task", err)
	}
	if ran != 0 {
		t.Errorf("the tool ran %d times, want 0: serial mode starts calls after the stream ends", ran)
	}
	if got := rec.only(t); got.id != "call_1" || got.text != errToolCallIncomplete.Error() {
		t.Errorf("settled call = %+v, want call_1 with the interruption explanation", got)
	}
}

// The other side of the rule: a call whose result did arrive is answered, not
// settled. Its window must keep the result it got.
func TestAnsweredToolCallIsNotSettled(t *testing.T) {
	fastRetries(t)
	p := &streamScriptProvider{script: []streamScript{{
		events: []StreamEvent{
			ToolInputStartEvent{ID: "call_1", Name: "write_file", Key: "tool:0", Position: 1},
			ToolInputDeltaEvent{ID: "call_1", Delta: `{"path":"/tmp/x"}`, Key: "tool:0"},
			ToolInputCompleteEvent{ID: "call_1", Key: "tool:0", Position: 1},
		},
		err: errors.New("stream vanished after the call completed"),
	}}}
	agent := NewAgent(AgentConfig{
		Provider: p,
		MaxSteps: 3,
		Tools: []Tool{{
			Definition: ToolDefinition{Name: "write_file"},
			Execute: func(context.Context, json.RawMessage) ([]ContentPart, error) {
				return []ContentPart{&TextPart{Text: "written"}}, nil
			},
		}},
	})
	rec := &settleRecorder{}

	result, err := agent.Stream(context.Background(), userText(), StreamCallbacks{
		OnRetry:      p.onRetry,
		OnBeforeSend: (&resumeCaller{}).onBeforeSend,
		OnToolOutput: rec.onToolOutput,
	})
	if err != nil {
		t.Fatalf("Stream error = %v, want the continuation to finish the task", err)
	}

	got := rec.only(t)
	if got.text != "written" {
		t.Errorf("tool output = %q, want the result itself — an answered call must not be settled", got.text)
	}
	// The salvage keeps a real pair: the call and its result stayed in history.
	if ids := toolCallIDs(result.Contents); len(ids) != 1 || ids[0] != "call_1" {
		t.Errorf("record holds tool calls %v, want [call_1] — the call that ran", ids)
	}
}
