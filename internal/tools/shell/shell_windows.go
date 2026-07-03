//go:build windows

package shell

import "os/exec"

// powershellBuildCmd is the shared command builder for PowerShell-based shells.
func powershellBuildCmd(binary, command string) *exec.Cmd {
	return exec.Command(binary, "-NoLogo", "-NonInteractive", "-Command", command)
}

// cmdBuildCmd is the command builder for cmd.exe.
func cmdBuildCmd(binary, command string) *exec.Cmd {
	return exec.Command(binary, "/C", command)
}

// ----- Windows shell definitions -----

var (
	shellPwsh = &Shell{
		Name:           "PowerShell Core",
		Binary:         "pwsh",
		PromptFragment: "Execute a command using PowerShell (pwsh). Commands run non-interactively. Programs expecting user input will hang.",
		BuildCmd:       powershellBuildCmd,
	}

	shellPowerShell = &Shell{
		Name:           "Windows PowerShell",
		Binary:         "powershell",
		PromptFragment: "Execute a command using Windows PowerShell. Commands run non-interactively. Programs expecting user input will hang.",
		BuildCmd:       powershellBuildCmd,
	}

	shellCmd = &Shell{
		Name:           "cmd",
		Binary:         "cmd",
		PromptFragment: "Execute a command using cmd.exe. No PowerShell cmdlets. Commands run non-interactively. Programs expecting user input will hang.",
		BuildCmd:       cmdBuildCmd,
	}
)

// knownShells lists shells in preference order for Windows.
// cmd.exe is always available on Windows, so the list is guaranteed to
// produce a match.
var knownShells = []*Shell{
	shellPwsh,
	shellPowerShell,
	shellCmd,
}
