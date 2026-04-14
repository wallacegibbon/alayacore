// Package shell provides cross-platform shell detection and command execution.
//
// # Design
//
// A Shell describes how to invoke a specific shell binary (bash, zsh, sh,
// pwsh, powershell).  Each shell carries:
//   - a binary name / path  (e.g. "/bin/bash", "pwsh")
//   - a prompt fragment     (the text injected into the tool description so
//     the LLM knows what syntax is available)
//   - an invocation builder (how to run "<shell> <flags> <command>")
//
// On startup the package probes the OS environment for available shells and
// selects the best candidate via [Detect].  The caller can override the
// choice by setting the ALAYACORE_SHELL environment variable.
package shell

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// Shell represents a command shell that can execute commands.
type Shell struct {
	// Name is a human-readable identifier (e.g. "bash", "PowerShell").
	Name string

	// Binary is the shell executable — either an absolute path or a binary
	// name that must be resolvable via PATH.
	Binary string

	// PromptFragment is appended to the execute_command tool description so
	// the LLM knows which syntax features are available.
	PromptFragment string

	// BuildCmd returns an *exec.Cmd that executes the given command string
	// inside this shell.
	BuildCmd func(binary, command string) *exec.Cmd
}

// detection stores the result of the one-time shell detection.
var detection struct {
	once  sync.Once
	shell *Shell
}

// knownShells lists shells in preference order. Detect() picks the first
// one whose binary is found.
var knownShells = []*Shell{
	{
		Name:   "bash",
		Binary: "bash",
		PromptFragment: `Execute a shell command.

Rules:
- Bash syntax is available (brace expansion, [[ ]], arrays, etc.)
- Prefer simple, standard commands over complex pipelines
- Quote filenames with spaces or special characters
- Check command output for errors before proceeding
- Clean up temporary files when done
- Commands run in a detached session with no controlling terminal and stdin closed. Interactive programs (sudo, ssh, etc.) that require a TTY or terminal input will fail immediately.`,
		BuildCmd: func(binary, command string) *exec.Cmd {
			return exec.Command(binary, "-c", command)
		},
	},
	{
		Name:   "zsh",
		Binary: "zsh",
		PromptFragment: `Execute a shell command.

Rules:
- Zsh syntax is available (brace expansion, [[ ]], arrays, etc.)
- Prefer simple, standard commands over complex pipelines
- Quote filenames with spaces or special characters
- Check command output for errors before proceeding
- Clean up temporary files when done
- Commands run in a detached session with no controlling terminal and stdin closed. Interactive programs (sudo, ssh, etc.) that require a TTY or terminal input will fail immediately.`,
		BuildCmd: func(binary, command string) *exec.Cmd {
			return exec.Command(binary, "-c", command)
		},
	},
	{
		Name:   "sh",
		Binary: "sh",
		PromptFragment: `Execute a shell command.

Rules:
- POSIX sh syntax is available
- Prefer simple, standard commands over complex pipelines
- Quote filenames with spaces or special characters
- Check command output for errors before proceeding
- Clean up temporary files when done
- Commands run in a detached session with no controlling terminal and stdin closed. Interactive programs (sudo, ssh, etc.) that require a TTY or terminal input will fail immediately.`,
		BuildCmd: func(binary, command string) *exec.Cmd {
			return exec.Command(binary, "-c", command)
		},
	},
	{
		Name:   "PowerShell Core",
		Binary: "pwsh",
		PromptFragment: `Execute a PowerShell command.

Rules:
- PowerShell (pwsh) syntax is available
- Prefer simple, standard commands over complex pipelines
- Quote filenames with spaces or special characters
- Check command output for errors before proceeding
- Clean up temporary files when done
- Commands run in a detached session with no controlling terminal and stdin closed. Interactive programs (sudo, ssh, etc.) that require a TTY or terminal input will fail immediately.`,
		BuildCmd: func(binary, command string) *exec.Cmd {
			return exec.Command(binary, "-NoLogo", "-NonInteractive", "-Command", command)
		},
	},
	{
		Name:   "Windows PowerShell",
		Binary: "powershell",
		PromptFragment: `Execute a PowerShell command.

Rules:
- Windows PowerShell syntax is available
- Prefer simple, standard commands over complex pipelines
- Quote filenames with spaces or special characters
- Check command output for errors before proceeding
- Clean up temporary files when done
- Commands run in a detached session with no controlling terminal and stdin closed. Interactive programs (sudo, ssh, etc.) that require a TTY or terminal input will fail immediately.`,
		BuildCmd: func(binary, command string) *exec.Cmd {
			return exec.Command(binary, "-NoLogo", "-NonInteractive", "-Command", command)
		},
	},
	{
		Name:   "cmd",
		Binary: "cmd",
		PromptFragment: `Execute a cmd.exe command.

Rules:
- Windows cmd.exe syntax is available (batch scripting, %VAR% expansion, etc.)
- Prefer simple, standard commands over complex pipelines
- Quote filenames with spaces or special characters
- Check command output for errors before proceeding
- Clean up temporary files when done
- Commands run in a detached session with no controlling terminal and stdin closed. Interactive programs that require a TTY or terminal input will fail immediately.`,
		BuildCmd: func(binary, command string) *exec.Cmd {
			return exec.Command(binary, "/C", command)
		},
	},
}

// lookPath reports whether binary can be found on PATH.
func lookPath(binary string) bool {
	_, err := exec.LookPath(binary)
	return err == nil
}

// Detect returns the best available shell for the current environment.
// It is safe to call from multiple goroutines; detection runs only once.
func Detect() *Shell {
	detection.once.Do(func() {
		detection.shell = detect()
	})
	return detection.shell
}

// detect performs the actual shell detection.
func detect() *Shell {
	// 1. Honor explicit override.
	if env := os.Getenv("ALAYACORE_SHELL"); env != "" {
		// If the user specified a known name (e.g. "bash"), look it up.
		lower := strings.ToLower(env)
		for _, s := range knownShells {
			if strings.EqualFold(s.Name, lower) || s.Binary == env {
				if lookPath(s.Binary) {
					return s
				}
			}
		}
		// Otherwise treat the value as an arbitrary shell binary.
		if lookPath(env) {
			return &Shell{
				Name:           env,
				Binary:         env,
				PromptFragment: fmt.Sprintf("Execute a command using %s.", env),
				BuildCmd: func(binary, command string) *exec.Cmd {
					return exec.Command(binary, "-c", command)
				},
			}
		}
	}

	// 2. OS-specific heuristic (implemented in shell_unix.go / shell_windows.go).
	if s := osDefault(); s != nil {
		if lookPath(s.Binary) {
			return s
		}
	}

	// 3. Try each known shell in preference order.
	for _, s := range knownShells {
		if lookPath(s.Binary) {
			return s
		}
	}

	// 4. Absolute last resort — plain "sh".
	return &Shell{
		Name:           "sh",
		Binary:         "sh",
		PromptFragment: "Execute a command using sh.",
		BuildCmd: func(binary, command string) *exec.Cmd {
			return exec.Command(binary, "-c", command)
		},
	}
}

// ResolvedBinary returns the absolute path to the shell binary after PATH
// resolution. Falls back to s.Binary if resolution fails.
func (s *Shell) ResolvedBinary() string {
	if path, err := exec.LookPath(s.Binary); err == nil {
		return path
	}
	return s.Binary
}

// IsWindows reports whether the current OS is Windows.
func IsWindows() bool {
	return runtime.GOOS == "windows"
}
