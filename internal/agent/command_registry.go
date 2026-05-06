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
	commandNameThink           = "think"
)

// Command describes a user-facing colon-command.  This is a pure metadata
// type — actual dispatch lives in Session.dispatchCommand.
type Command struct {
	Name        string // Command name (without colon)
	Description string // Short description for help
	Usage       string // Usage example (e.g. "<id>")
}

// commandDefs is the single source of truth for all colon-commands.
// Order here determines the order used by LookupCommand iteration.
var commandDefs = []Command{
	{commandNameSummarize, "Summarize the conversation to reduce context", ""},
	{commandNameCancel, "Cancel the current task", ""},
	{commandNameCancelAll, "Cancel current task and clear the task queue", ""},
	{commandNameContinue, "Resume after an error; without args retries the prompt, with 'skip' skips it", "[skip]"},
	{commandNameSave, "Save the current session", "[filename]"},
	{commandNameModelSet, "Switch to a different model", "<id>"},
	{commandNameModelLoad, "Reload models from configuration file", ""},
	{commandNameTaskQueueGetAll, "List all queued tasks", ""},
	{commandNameTaskQueueDel, "Delete a queued task", "<queue_id>"},
	{commandNameThink, "Set think level (0=off, 1=normal, 2=max)", "[0|1|2]"},
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

// ListCommands returns all registered command metadata.
func ListCommands() []Command {
	return commandDefs
}

// DispatchCommand dispatches a colon-command to the appropriate Session method.
// Returns true if the command was recognized (even if execution failed).
func (s *Session) dispatchCommand(ctx context.Context, cmd string) bool {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		s.writeError(domainerrors.ErrEmptyCommand.Error())
		return false
	}

	commandName := parts[0]
	args := parts[1:]

	if _, ok := LookupCommand(commandName); !ok {
		return false
	}

	// Dispatch to the handler methods.
	switch commandName {
	case commandNameSummarize:
		s.summarize(ctx)
	case commandNameCancel:
		s.cancelTask()
	case commandNameCancelAll:
		s.cancelAllTasks()
	case commandNameContinue:
		s.handleContinue(ctx, args)
	case commandNameSave:
		s.saveSession(args)
	case commandNameModelSet:
		s.handleModelSet(args)
	case commandNameModelLoad:
		s.handleModelLoad()
	case commandNameTaskQueueGetAll:
		s.handleTaskQueueGetAll()
	case commandNameTaskQueueDel:
		s.handleTaskQueueDel(args)
	case commandNameThink:
		s.handleThink(args)
	}

	return true
}
