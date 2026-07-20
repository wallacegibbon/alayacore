package agent

// Command registry: define, register, and dispatch colon-commands.
//
// Previously a flat package-level slice + LookupCommand function,
// now wrapped in a CommandRegistry struct for cleaner encapsulation.
// The package-level LookupCommand still works via the default registry.

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
	CommandNameMCPConfirm  = "mcp_confirm"
	CommandNameMCPDecline  = "mcp_decline"
	CommandNameMCPSkip     = "mcp_cancel"
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
type CommandHandler func(s *Session, ctx context.Context, args string)

// Command describes a user-facing colon-command with its handler and metadata.
type Command struct {
	Name        string
	Description string
	Usage       string
	Policy      CmdPolicy
	Handler     CommandHandler
}

// CommandRegistry manages the set of available colon-commands.
type CommandRegistry struct {
	commands map[string]Command
}

// NewCommandRegistry creates a registry pre-populated with all built-in commands.
func NewCommandRegistry() *CommandRegistry {
	cr := &CommandRegistry{
		commands: make(map[string]Command, len(defaultCommandDefs)),
	}
	for _, cmd := range defaultCommandDefs {
		cr.commands[cmd.Name] = cmd
	}
	return cr
}

// Lookup returns the command metadata for name, or (nil, false).
func (cr *CommandRegistry) Lookup(name string) (*Command, bool) {
	cmd, ok := cr.commands[name]
	if !ok {
		return nil, false
	}
	return &cmd, true
}

// Names returns all registered command names (for help display, etc.).
func (cr *CommandRegistry) Names() []string {
	names := make([]string, 0, len(cr.commands))
	for name := range cr.commands {
		names = append(names, name)
	}
	return names
}

// Register adds a command to the registry. Panics on duplicate name.
func (cr *CommandRegistry) Register(cmd Command) {
	if _, ok := cr.commands[cmd.Name]; ok {
		panic("command already registered: " + cmd.Name)
	}
	cr.commands[cmd.Name] = cmd
}

// LookupCommand is a package-level shorthand for the default registry.
func LookupCommand(name string) (*Command, bool) {
	return defaultCommandRegistry.Lookup(name)
}

// defaultCommandRegistry is the package-level singleton.
var defaultCommandRegistry = NewCommandRegistry()

// defaultCommandDefs is the list of all built-in commands.
var defaultCommandDefs = []Command{
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
	{CommandNameMCPConfirm, "Confirm MCP OAuth authorization with auth code", "<server> <code> <redirect_uri>", CmdIdle,
		func(s *Session, ctx context.Context, args string) { s.handleMCPConfirm(ctx, args) }},
	{CommandNameMCPDecline, "Decline MCP OAuth authorization", "<server>", CmdIdle,
		func(s *Session, _ context.Context, args string) { s.handleMCPDecline(args) }},
	{CommandNameMCPSkip, "Cancel MCP initialization", "", CmdImmediate,
		func(s *Session, _ context.Context, _ string) { s.handleMCPCancel() }},
}
