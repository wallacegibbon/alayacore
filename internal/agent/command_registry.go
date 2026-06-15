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
	CommandNameFork            = "fork"
)

// SchedulePolicy specifies how and when a command is dispatched.
type SchedulePolicy int

const (
	// ScheduleDeferred commands are enqueued as tasks and executed
	// in the task goroutine.  Unknown commands also receive this
	// policy so they fall through to the deferred path where
	// runTaskCommand handles summarize and continue.
	ScheduleDeferred SchedulePolicy = iota

	// ScheduleImmediate commands run synchronously in the run()
	// goroutine.  They are safe to execute even while a task is
	// streaming (e.g. :cancel, :save, :reason).
	ScheduleImmediate

	// ScheduleWhenIdle commands run synchronously in the run()
	// goroutine but are rejected when a task is in progress
	// because they mutate state that the task goroutine reads
	// (model configuration, agent/provider pointers).
	ScheduleWhenIdle
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
	Schedule    SchedulePolicy // When the command can be dispatched
	Handler     CommandHandler // The handler function
}

// commandDefs is the single source of truth for all colon-commands.
// Order here determines the order used by LookupCommand iteration.
var commandDefs = []Command{
	{CommandNameCancel, "Cancel the current task", "", ScheduleImmediate,
		func(s *Session, _ context.Context, _ []string) { s.cancelTask() }},
	{CommandNameCancelAll, "Cancel current task and clear the task queue", "", ScheduleImmediate,
		func(s *Session, _ context.Context, _ []string) { s.cancelAllTasks() }},
	{CommandNameSave, "Save the current session", "[filename]", ScheduleImmediate,
		func(s *Session, _ context.Context, args []string) { s.saveSession(args) }},
	{CommandNameModelSet, "Switch to a different model", "<id>", ScheduleWhenIdle,
		func(s *Session, _ context.Context, args []string) { s.handleModelSet(args) }},
	{CommandNameModelLoad, "Reload models from configuration file", "", ScheduleWhenIdle,
		func(s *Session, _ context.Context, _ []string) { s.handleModelLoad() }},
	{CommandNameTaskQueueGetAll, "List all queued tasks", "", ScheduleImmediate,
		func(s *Session, _ context.Context, _ []string) { s.handleTaskQueueGetAll() }},
	{CommandNameTaskQueueDel, "Delete a queued task", "<queue_id>", ScheduleImmediate,
		func(s *Session, _ context.Context, args []string) { s.handleTaskQueueDel(args) }},
	{CommandNameTaskQueueEdit, "Edit a queued task's content", "<queue_id> <new_content>", ScheduleImmediate,
		func(s *Session, _ context.Context, args []string) { s.handleTaskQueueEdit(args) }},
	{CommandNameReason, "Set reasoning level (0=off, 1=normal, 2=max)", "[0|1|2]", ScheduleImmediate,
		func(s *Session, _ context.Context, args []string) { s.handleReason(args) }},
	{CommandNameThemeSet, "Set the active theme", "<name>", ScheduleImmediate,
		func(s *Session, _ context.Context, args []string) { s.handleThemeSet(args) }},
	{CommandNameConfirm, "Confirm or deny a pending tool execution", "yes|no", ScheduleImmediate,
		func(s *Session, _ context.Context, args []string) { s.handleConfirmCommand(args) }},
	{CommandNameFork, "Fork session up to content ID and save to file", "<id> <filename>", ScheduleImmediate,
		func(s *Session, _ context.Context, args []string) { s.handleFork(args) }},
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

// LookupSchedule returns the SchedulePolicy for the given command string.
// Returns ScheduleDeferred for unknown commands so they fall through to
// the deferred-command path (handles :summarize and :continue).
func LookupSchedule(cmd string) SchedulePolicy {
	name := cmd
	if idx := strings.IndexByte(cmd, ' '); idx >= 0 {
		name = cmd[:idx]
	}
	if c, ok := LookupCommand(name); ok {
		return c.Schedule
	}
	return ScheduleDeferred
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
