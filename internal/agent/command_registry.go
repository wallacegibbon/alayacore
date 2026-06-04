package agent

import (
	"context"
	"strings"

	domainerrors "github.com/alayacore/alayacore/internal/errors"
)

// Command name constants
const (
	CommandNameSummarize       = "summarize"
	CommandNameCancel          = "cancel"
	CommandNameCancelAll       = "cancel_all"
	CommandNameContinue        = "continue"
	CommandNameSave            = "save"
	CommandNameModelSet        = "model_set"
	CommandNameModelLoad       = "model_load"
	CommandNameTaskQueueGetAll = "taskqueue_get_all"
	CommandNameTaskQueueDel    = "taskqueue_del"
	CommandNameTaskQueueEdit   = "taskqueue_edit"
	CommandNameReason          = "reason"
	CommandNameThemeSet        = "theme_set"
	CommandNameConfirm         = "confirm"
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
	{CommandNameCancel, "Cancel the current task", "", true,
		func(s *Session, _ context.Context, _ []string) { s.cancelTask() }},
	{CommandNameCancelAll, "Cancel current task and clear the task queue", "", true,
		func(s *Session, _ context.Context, _ []string) { s.cancelAllTasks() }},
	{CommandNameSave, "Save the current session", "[filename]", true,
		func(s *Session, _ context.Context, args []string) { s.saveSession(args) }},
	{CommandNameModelSet, "Switch to a different model", "<id>", true,
		func(s *Session, _ context.Context, args []string) { s.handleModelSet(args) }},
	{CommandNameModelLoad, "Reload models from configuration file", "", true,
		func(s *Session, _ context.Context, _ []string) { s.handleModelLoad() }},
	{CommandNameTaskQueueGetAll, "List all queued tasks", "", true,
		func(s *Session, _ context.Context, _ []string) { s.handleTaskQueueGetAll() }},
	{CommandNameTaskQueueDel, "Delete a queued task", "<queue_id>", true,
		func(s *Session, _ context.Context, args []string) { s.handleTaskQueueDel(args) }},
	{CommandNameTaskQueueEdit, "Edit a queued task's content", "<queue_id> <new_content>", true,
		func(s *Session, _ context.Context, args []string) { s.handleTaskQueueEdit(args) }},
	{CommandNameReason, "Set reasoning level (0=off, 1=normal, 2=max)", "[0|1|2]", true,
		func(s *Session, _ context.Context, args []string) { s.handleReason(args) }},
	{CommandNameThemeSet, "Set the active theme", "<name>", true,
		func(s *Session, _ context.Context, args []string) { s.handleThemeSet(args) }},
	{CommandNameConfirm, "Confirm or deny a pending tool execution", "yes|no", true,
		func(s *Session, _ context.Context, args []string) { s.handleConfirmCommand(args) }},
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
