//go:build windows

package shell

// osDefault returns the preferred shell for Windows.
// pwsh (PowerShell Core) > powershell (Windows PowerShell) > cmd.
// detect() will fall through to later candidates if the preferred one
// is not installed.
func osDefault() *Shell {
	return knownShells[3] // pwsh
}
