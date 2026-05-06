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

// Adaptor reads prompts from stdin and prints assistant output to stdout.
type Adaptor struct {
	Config *app.Config
}

// NewAdaptor creates a new plainio adaptor.
func NewAdaptor(cfg *app.Config) *Adaptor {
	return &Adaptor{Config: cfg}
}

// Start runs the plainio adaptor. It blocks until the session finishes.
// Returns the exit code: 0 for graceful exit, 1 for Ctrl-C or errors.
//
// plainio processes prompts one at a time. If a task produces an error
// (SE tag), the remaining input is discarded and the process exits
// with code 1 — queued tasks are NOT executed.
func (a *Adaptor) Start() int {
	input := stream.NewChanInput(100)
	output := newStdoutOutput()

	// Load session
	session, _ := agentpkg.LoadOrNewSession(agentpkg.SessionConfig{
		Input:              input,
		Output:             output,
		SessionFile:        a.Config.Cfg.Session,
		ModelConfigPath:    a.Config.Cfg.ModelConfig,
		RuntimeConfigPath:  a.Config.Cfg.RuntimeConfig,
		BaseTools:          a.Config.AgentTools,
		SystemPrompt:       a.Config.SystemPrompt,
		ExtraSystemPrompt:  a.Config.ExtraSystemPrompt,
		MaxSteps:           a.Config.MaxSteps,
		DebugAPI:           a.Config.Cfg.DebugAPI,
		AutoSummarize:      a.Config.Cfg.AutoSummarize,
		NoCompact:          a.Config.Cfg.NoCompact,
		CompactKeepSteps:   a.Config.Cfg.CompactKeepSteps,
		CompactTruncateLen: a.Config.Cfg.CompactTruncateLen,
		ProxyURL:           a.Config.Cfg.Proxy,
		SkillsMgr:          a.Config.SkillsMgr,
	})

	// Check if we have any models available.
	if !session.HasModels() {
		fmt.Fprint(os.Stderr, agentpkg.NoModelsErrorMessage(session.ModelConfigPath()))
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
	// First exit trigger wins: EOF (0), error (1), or Ctrl-C (1).
	code := 0
	select {
	case code = <-exitCh:
	case <-sigCh:
		code = 1
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
		code = 1
	}

	// Final check: even on a clean EOF path the session may have written
	// errors (network failures, API errors, etc.) that arrived after the
	// stdin goroutine closed input.  Override the exit code.
	if code == 0 && output.HasError() {
		code = 1
	}

	return code
}
