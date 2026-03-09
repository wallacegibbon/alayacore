package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"charm.land/fantasy"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// PosixShellInput represents the input for the posix_shell tool
type PosixShellInput struct {
	Command string `json:"command" description:"The shell command to execute"`
}

// NewPosixShellTool creates a new posix_shell tool for executing shell commands
func NewPosixShellTool() fantasy.AgentTool {
	return fantasy.NewAgentTool(
		"posix_shell",
		"Execute a shell command in the terminal",
		func(ctx context.Context, input PosixShellInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			cmd := input.Command
			if cmd == "" {
				return fantasy.NewTextErrorResponse("command is required"), nil
			}

			var stdout, stderr bytes.Buffer

			parser := syntax.NewParser()
			prog, err := parser.Parse(strings.NewReader(cmd), "")
			if err != nil {
				return fantasy.NewTextErrorResponse("parse error: " + err.Error()), nil
			}

			cwd, _ := os.Getwd()
			runner, err := interp.New(
				interp.Dir(cwd),
				interp.Env(expand.ListEnviron(os.Environ()...)),
				interp.StdIO(os.Stdin, &stdout, &stderr),
				interp.ExecHandlers(),
			)
			if err != nil {
				return fantasy.NewTextErrorResponse("failed to create runner: " + err.Error()), nil
			}

			err = runner.Run(ctx, prog)
			output := stdout.String()
			if stderr.Len() > 0 {
				if output != "" {
					output += "\n"
				}
				output += stderr.String()
			}

			if err != nil {
				var exitStatus interp.ExitStatus
				if errors.As(err, &exitStatus) {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("[%d] %s", exitStatus, output)), nil
				}
				if output != "" {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("%s\n%s", err.Error(), output)), nil
				}
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}

			return fantasy.NewTextResponse(output), nil
		},
	)
}
