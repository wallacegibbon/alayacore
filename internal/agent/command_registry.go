package agent

import (
	"context"
)

// Command name constants
const (
	CommandNameSummarize   = "summarize"
	CommandNameCancel      = "cancel"
	CommandNameContinue    = "continue"
	CommandNameSave        = "save"
	CommandNameModelSet    = "model_set"
	CommandNameModelLoad   = "model_load"
	CommandNameModelSync   = "model_sync"
	CommandNameReason      = "reason"
	CommandNameThemeSet    = "theme_set"
	CommandNameConfirm     = "confirm"
	CommandNameFork        = "fork"
	CommandNameVideoConfig = "video_config"
)

// CmdPolicy specifies how and when a command is dispatched.
type CmdPolicy int

const (
	// CmdImmediate runs synchronously in the run() goroutine.
	// Safe to execute even while a task is streaming (e.g. :cancel, :save).
	CmdImmediate CmdPolicy = iota

	// CmdIdle runs synchronously but is rejected when a task is
	// in progress (e.g. :model_set changes state the task reads).
	CmdIdle
)

// CommandHandler is a function that handles a colon-command.
// args is everything after the first space (empty string if no args).
// Each handler parses args as appropriate for its command.
type CommandHandler func(s *Session, ctx context.Context, args string)

// Command describes a user-facing colon-command with its handler and metadata.
type Command struct {
	Name        string
	Description string
	Usage       string
	Policy      CmdPolicy
	Handler     CommandHandler
}

// commandDefs is the single source of truth for all colon-commands.
var commandDefs = []Command{
	{CommandNameCancel, "Cancel the current task", "", CmdImmediate,
		func(s *Session, _ context.Context, _ string) { s.cancelTask() }},
	{CommandNameSave, "Save the current session", "[filename]", CmdImmediate,
		func(s *Session, _ context.Context, args string) { s.saveSession(args) }},
	{CommandNameModelSet, "Switch to a different model", "<id>", CmdIdle,
		func(s *Session, _ context.Context, args string) { s.handleModelSet(args) }},
	{CommandNameModelLoad, "Reload models from configuration file", "", CmdIdle,
		func(s *Session, _ context.Context, _ string) { s.handleModelLoad() }},
	{CommandNameModelSync, "Replace all models with edited content", "<content>", CmdIdle,
		func(s *Session, _ context.Context, args string) { s.handleModelSync(args) }},
	{CommandNameReason, "Set reasoning level (0=off, 1=normal, 2=max)", "[0|1|2]", CmdIdle,
		func(s *Session, _ context.Context, args string) { s.handleReason(args) }},
	{CommandNameThemeSet, "Set the active theme", "<name>", CmdImmediate,
		func(s *Session, _ context.Context, args string) { s.handleThemeSet(args) }},
	{CommandNameConfirm, "Confirm or deny a pending tool execution", "yes|no", CmdImmediate,
		func(s *Session, _ context.Context, args string) { s.handleConfirmCommand(args) }},
	{CommandNameFork, "Fork session up to content ID and save to file", "<id> <filename>", CmdImmediate,
		func(s *Session, _ context.Context, args string) { s.handleFork(args) }},
	{CommandNameVideoConfig, "Set video FPS and resolution", "<fps> <0|1>", CmdIdle,
		func(s *Session, _ context.Context, args string) { s.handleVideoConfig(args) }},
}

// LookupCommand returns the command metadata for name, or (nil, false).
func LookupCommand(name string) (*Command, bool) {
	for i := range commandDefs {
		if commandDefs[i].Name == name {
			return &commandDefs[i], true
		}
	}
	return nil, false
}
