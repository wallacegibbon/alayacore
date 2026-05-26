package agent

import (
	"context"
	"strings"

	domainerrors "github.com/alayacore/alayacore/internal/errors"
)

// Command name constants
const (
	commandNameSummarize       = "summarize"
	commandNameCancel          = "cancel"
	commandNameCancelAll       = "cancel_all"
	commandNameContinue        = "continue"
	commandNameSave            = "save"
	commandNameModelSet        = "model_set"
	commandNameModelLoad       = "model_load"
	commandNameTaskQueueGetAll = "taskqueue_get_all"
	commandNameTaskQueueDel    = "taskqueue_del"
	commandNameTaskQueueEdit   = "taskqueue_edit"
	commandNameReason          = "reason"
	commandNameThemeSet        = "theme_set"
)

// CommandHandler is a function that handles a colon-command.
// The session, context, and parsed arguments are passed in.
// Unused parameters can be ignored by the handler.
type CommandHandler func(s *Session, ctx context.Context, args []string)

// Command describes a user-facing colon-command with its handler and metadata.
type Command struct {
	Name        string         // Command name (without colon)
	Description string         // Short description for help
	Usage       string         // Usage example (e.g. "<id>")
	Immediate   bool           // True if handled immediately (not queued)
	Handler     CommandHandler // The handler function
}

// commandDefs is the single source of truth for all colon-commands.
// Order here determines the order used by LookupCommand iteration.
var commandDefs = []Command{
	{commandNameCancel, "Cancel the current task", "", true,
		func(s *Session, _ context.Context, _ []string) { s.cancelTask() }},
	{commandNameCancelAll, "Cancel current task and clear the task queue", "", true,
		func(s *Session, _ context.Context, _ []string) { s.cancelAllTasks() }},
	{commandNameSave, "Save the current session", "[filename]", true,
		func(s *Session, _ context.Context, args []string) { s.saveSession(args) }},
	{commandNameModelSet, "Switch to a different model", "<id>", true,
		func(s *Session, _ context.Context, args []string) { s.handleModelSet(args) }},
	{commandNameModelLoad, "Reload models from configuration file", "", true,
		func(s *Session, _ context.Context, _ []string) { s.handleModelLoad() }},
	{commandNameTaskQueueGetAll, "List all queued tasks", "", true,
		func(s *Session, _ context.Context, _ []string) { s.handleTaskQueueGetAll() }},
	{commandNameTaskQueueDel, "Delete a queued task", "<queue_id>", true,
		func(s *Session, _ context.Context, args []string) { s.handleTaskQueueDel(args) }},
	{commandNameTaskQueueEdit, "Edit a queued task's content", "<queue_id> <new_content>", true,
		func(s *Session, _ context.Context, args []string) { s.handleTaskQueueEdit(args) }},
	{commandNameReason, "Set reasoning level (0=off, 1=normal, 2=max)", "[0|1|2]", true,
		func(s *Session, _ context.Context, args []string) { s.handleReason(args) }},
	{commandNameThemeSet, "Set the active theme", "<name>", true,
		func(s *Session, _ context.Context, args []string) { s.handleThemeSet(args) }},
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

// IsImmediate reports whether the given command string should be handled
// immediately without queuing. Returns false for unknown commands.
func IsImmediate(cmd string) bool {
	name := cmd
	if idx := strings.IndexByte(cmd, ' '); idx >= 0 {
		name = cmd[:idx]
	}
	if c, ok := LookupCommand(name); ok {
		return c.Immediate
	}
	return false
}

// DispatchCommand dispatches a colon-command to its registered handler.
// Returns true if the command was recognized (even if execution failed).
func (s *Session) dispatchCommand(ctx context.Context, cmd string) bool {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		s.writeError(domainerrors.ErrEmptyCommand.Error())
		return false
	}

	commandName := parts[0]
	args := parts[1:]

	c, ok := LookupCommand(commandName)
	if !ok || c.Handler == nil {
		return false
	}

	c.Handler(s, ctx, args)
	return true
}
