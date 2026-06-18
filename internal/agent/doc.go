// Package agent provides the core session management for AlayaCore.
//
// The agent package implements the session layer that sits between the
// adapters (terminal/plainio) and the AI model provider. It handles:
//
//   - Task queue management (prompts and commands)
//   - Model interaction and streaming
//   - Context management and auto-summarization (opt-in via --auto-summarize flag)
//   - Command processing (:save, :model_set, :taskqueue_*, etc.)
//   - Session persistence (save/load conversations)
//
// Data Model:
//
//	The session stores conversation history as a flat, ordered slice of
//	ContentPart, where each item has a stable ID matching the TLV stream ID
//	sent to the adapter. This enables the adapter to reference individual
//	content blocks by ID (e.g. ":save 5") without any secondary index.
//	For LLM API calls, ContentParts are grouped by role into []llm.Message
//	on the fly at the task boundary.
//
// Concurrency Model:
//
//	The session uses an actor model with three goroutines:
//	  1. run() — owns all mutable session state (Content, task queue,
//	     token counts). Processes input messages and task events.
//	     When the input stream reaches EOF while a task is in progress,
//	     it drains remaining events until the task completes before
//	     exiting (see drainUntilTaskDone).
//	  2. task goroutine — spawned per task, runs in background, sends
//	     state mutations via typed channel events (taskEventCh) to run().
//	     On completion it sends a TaskResult with the final messages
//	     and new ContentParts back via taskResultCh.
//	  3. inputPump — reads TLV frames from input, forwards to run()
//	     via a message channel.
//
//	The only mutable state accessed from more than one goroutine are:
//	  - atomic fields for pausedOnError, outputBroken, and confirmCh
//	  - A few buffered channels for cancellation, completion signaling,
//	    and system-info refresh requests.
//
//	All other session state (agent, provider, ContextTokens, ContextLimit,
//	reasoningLevel, histCounter) is owned by a single goroutine and
//	accessed without synchronization.
//
//	Cross-goroutine
//	communication is exclusively through channels and atomics.
//
// Architecture Overview:
//
//	Session wires together the model, tools, IO streams, and managers.
//	The active model is resolved by priority (highest first):
//
//	  --model CLI flag
//	  session file frontmatter  (when loading via --session)
//	  runtime.conf              (global default)
//	  model.conf first entry    (fallback)
//
//	Model switching is scoped: sessions with a file-specified model
//	store switches in-memory (saved to the session file on :save),
//	while sessions without one write to the global runtime.conf.
//
//	  --model flag ──────────────────────┐
//	                                     │
//	  session file ──▶ SessionMeta ──────┤ override
//	                                     │
//	  runtime.conf ──▶ RuntimeManager ───┤ active_model
//	                                     │
//	  model.conf ────▶ ModelManager ─────┤ fallback
//	                                     │
//	                                     ▼
//	                               Session.activeModel
//
// Communication Protocol:
//
//	Adapters communicate with Session via TLV (Tag-Length-Value) streams:
//	  - Input: TagUserT for prompts and commands, TagUserI for images,
//	    TagUserV for videos, TagUserA for audio, TagUserD for documents
//	  - Output: TagAssistantT, TagAssistantR, TagAssistantF, etc.
//
//	Each TLV frame carries a NUL-delimited history ID prefix that the
//	adapter uses to route content to display windows. These IDs correspond
//	directly to ContentPart.GetHistoryID() in the session's content store.
//
// Key Components:
//
//   - Session: Main session struct managing conversation state
//   - ContentPart: Atomic unit of conversation content with stable ID
//   - ModelManager: Loads and manages AI model configurations.
//     Rejects models with invalid protocol_type, base_url, or model_name.
//     Use GetLoadErrors() to retrieve validation messages.
//   - RuntimeManager: Persists runtime settings (active model)
//   - Task Queue: FIFO queue for pending prompts/commands
//   - Command Registry: Declarative command registration
//
// Key Files:
//
//   - session.go: Session struct, lifecycle, and cross-goroutine channels
//   - session_task.go: Prompt processing, agent loop, OnStepFinish ContentPart building
//   - session_queue.go: Task queue, task runner, task commands
//   - session_loop.go: Main event loop, task start/done
//   - session_io.go: TLV input/output, summarize, continue commands
//   - session_content.go: ContentPart helpers, tag mapping, ID lookup
//   - session_persist.go: Session save/load functionality
//   - session_types.go: Type definitions (ContentPart, TaskResult, etc.)
//   - command_registry.go: Declarative command registration
//   - model_manager.go: Model configuration management
//   - runtime_manager.go: Runtime persistence
//
// Usage:
//
//	input := stream.NewSliceBuffer(10)
//	output := &bufferOutput{}
//	cfg := agent.SessionConfig{Input: input, Output: output, ...}
//	session := agent.NewSession(cfg)
//	session.Start()
package agent
