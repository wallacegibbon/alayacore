//go:build !windows

package shell

import "os/exec"

// unixBuildCmd is the shared command builder for all Unix shells.
// They all use the same "-c" flag to run a command string.
func unixBuildCmd(binary, command string) *exec.Cmd {
	return exec.Command(binary, "-c", command)
}

// ----- Unix shell definitions -----

var (
	shellBash = &Shell{
		Name:           "bash",
		Binary:         "bash",
		PromptFragment: "Execute a shell command using bash. Arrays are 0-indexed (${array[0]}). Commands run non-interactively (stdin from /dev/null). Programs expecting user input (e.g. sudo) will hang.",
		BuildCmd:       unixBuildCmd,
	}

	shellZsh = &Shell{
		Name:           "zsh",
		Binary:         "zsh",
		PromptFragment: "Execute a shell command using zsh. Arrays are 1-indexed (${array[1]}). Commands run non-interactively (stdin from /dev/null). Programs expecting user input (e.g. sudo) will hang.",
		BuildCmd:       unixBuildCmd,
	}

	shellSh = &Shell{
		Name:           "sh",
		Binary:         "sh",
		PromptFragment: "Execute a shell command using POSIX sh. No arrays, no [[ ]], no brace expansion. Commands run non-interactively (stdin from /dev/null). Programs expecting user input (e.g. sudo) will hang.",
		BuildCmd:       unixBuildCmd,
	}
)

// knownShells lists shells in preference order for Unix-like systems.
// sh is always available on POSIX systems, so the list is guaranteed to
// produce a match.
var knownShells = []*Shell{
	shellBash,
	shellZsh,
	shellSh,
}
