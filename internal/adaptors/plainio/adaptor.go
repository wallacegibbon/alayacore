package plainio

// Package plainio provides a plain stdin/stdout adaptor for AlayaCore.
// It reads prompts from stdin (one per line) and prints messages to stdout.
// No terminal features are used — just plain IO.

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/stream"
)

// Compile-time check: Adaptor satisfies app.Adaptor.
var _ app.Adaptor = (*Adaptor)(nil)

// Adaptor reads prompts from stdin and prints assistant output to stdout.
type Adaptor struct {
	Config *app.Config
}

// NewAdaptor creates a new plainio adaptor.
func NewAdaptor(cfg *app.Config) *Adaptor {
	return &Adaptor{Config: cfg}
}

// Start runs the plainio adaptor. It blocks until the session finishes.
// Returns the exit code: 0 for graceful exit, 1 for errors, 130 (128+SIGINT)
// for Ctrl-C.
//
// plainio processes prompts one at a time. If a task produces an error
// (SE tag), the remaining input is discarded and the process exits
// with code 1 — queued tasks are NOT executed.
func (a *Adaptor) Start() int {
	input := stream.NewChanInput(100)
	output := newStdoutOutput()

	// Load session
	session, _ := agentpkg.LoadOrNewSession(agentpkg.SessionConfig{
		Input:               input,
		Output:              output,
		SessionFile:         a.Config.Cfg.Session,
		ModelConfigPath:     a.Config.Cfg.ModelConfig,
		RuntimeConfigPath:   a.Config.Cfg.RuntimeConfig,
		BaseTools:           a.Config.AgentTools,
		SystemPrompt:        a.Config.SystemPrompt,
		ExtraSystemPrompt:   a.Config.ExtraSystemPrompt,
		MaxSteps:            a.Config.MaxSteps,
		DebugAPI:            a.Config.Cfg.DebugAPI,
		AutoSummarize:       a.Config.Cfg.AutoSummarize,
		ProxyURL:            a.Config.Cfg.Proxy,
		SkillsMgr:           a.Config.SkillsMgr,
		OverrideActiveModel: a.Config.Cfg.ModelName,
	})

	// --model CLI flag: fail immediately if the named model doesn't exist.
	if err := session.InitError(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	// Display config validation messages (unknown protocol_type, missing fields, etc.)
	// Must come before HasModels() check so specific errors are shown even when
	// all models are rejected.
	if msgs := session.ModelManager.GetLoadErrors(); len(msgs) > 0 {
		for _, m := range msgs {
			fmt.Fprintf(os.Stderr, "%s\n", m)
		}
	}

	// Check if we have any models available.
	if !session.HasModels() {
		fmt.Fprint(os.Stderr, agentpkg.NoModelsErrorMessage(session.ModelConfigPath(), session.ModelManager.HasRejected()))
		return 1
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT)
	defer signal.Stop(sigCh)

	exitCh := make(chan int, 1)

	// Read stdin and emit TLV messages.
	go func() {
		if err := readPromptsFromStdin(input); err != nil {
			select {
			case exitCh <- 1:
			default:
			}
			return
		}
		input.Close()
		select {
		case exitCh <- 0:
		default:
		}
	}()

	// Watch for errors — close input so queued tasks are abandoned.
	go func() {
		output.WaitForError()
		input.Close()
		select {
		case exitCh <- 1:
		default:
		}
	}()

	// Main goroutine owns all signal handling. No SIGINT goroutine.
	// First exit trigger wins: EOF (0), error (1), or Ctrl-C (130).
	code := 0
	select {
	case code = <-exitCh:
	case <-sigCh:
		code = 128 + int(syscall.SIGINT)
		input.Close()
	}

	// Let the current task finish, but a second Ctrl-C forces immediate exit.
	done := make(chan struct{})
	go func() {
		session.WaitDone()
		close(done)
	}()
	select {
	case <-done:
	case <-sigCh:
		code = 128 + int(syscall.SIGINT)
	}

	// Final check: even on a clean EOF path the session may have written
	// errors (network failures, API errors, etc.) that arrived after the
	// stdin goroutine closed input.  Override the exit code.
	if code == 0 && output.HasError() {
		code = 1
	}

	return code
}
