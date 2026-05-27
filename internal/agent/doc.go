// Package agent provides the core session management for AlayaCore.
//
// The agent package implements the session layer that sits between the
// adaptors (terminal/plainio) and the AI model provider. It handles:
//
//   - Task queue management (prompts and commands)
//   - Model interaction and streaming
//   - Context management and auto-summarization (opt-in via --auto-summarize flag)
//   - Command processing (:save, :model_set, :taskqueue_*, etc.)
//   - Session persistence (save/load conversations)
//
// Concurrency Model:
//
//	The session uses an actor model with three goroutines:
//	  1. run() — owns all mutable session state (messages, task queue,
//	     token counts). Processes input messages and task events.
//	     When the input stream reaches EOF while a task is in progress,
//	     it drains remaining events until the task completes before
//	     exiting (see drainUntilTaskDone).
//	  2. task goroutine — spawned per task, runs in background, sends
//	     state mutations via typed channel events (stateCh) to run().
//	  3. inputPump — reads TLV frames from input, forwards to run()
//	     via a message channel.
//
//	The only mutable state accessed from more than one goroutine are:
//	  - sync/atomic fields for lock-free reads by the task goroutine
//	    (agent pointer, provider, reasoning level, context tokens, etc.)
//	  - A few buffered channels for cancellation, completion signaling,
//	    and system-info refresh requests.
//
//	Cross-goroutine
//	communication is exclusively through channels and atomics.
//
// Architecture Overview:
//
//	Session wires together the model, tools, IO streams, and managers:
//	  model.conf --(ModelManager)--> available models
//	        ^                               |
//	        |                               v
//	  runtime.conf --(RuntimeManager)--> active model name
//	        |                               |
//	        +--------(Session)--------------+
//
// Communication Protocol:
//
//	Adaptors communicate with Session via TLV (Tag-Length-Value) streams:
//	  - Input: TagTextUser for prompts and commands
//	  - Output: TagTextAssistant, TagTextReasoning, TagFunctionCall, etc.
//
// Key Components:
//
//   - Session: Main session struct managing conversation state
//   - ModelManager: Loads and manages AI model configurations.
//     Rejects models with invalid protocol_type, base_url, or model_name.
//     Use GetLoadErrors() to retrieve validation messages.
//   - RuntimeManager: Persists runtime settings (active model)
//   - Task Queue: FIFO queue for pending prompts/commands
//   - Command Registry: Declarative command registration
//
// Key Files:
//
//   - session.go: Session struct, main loop, and command handling
//   - session_io.go: TLV input/output handling
//   - session_persist.go: Session save/load functionality
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
