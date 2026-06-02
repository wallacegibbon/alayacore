package terminal

// Key string constants for the terminal UI.
// All raw key strings used in key handling should be defined here.
// This ensures a single source of truth for key bindings.

const (
	// Navigation keys
	keyUp     = "up"
	keyDown   = "down"
	keyLeft   = "left"
	keyRight  = "right"
	keyTab    = "tab"
	keyEnter  = "enter"
	keyEsc    = "esc"
	keySpace  = "space"
	keyHome   = "home"
	keyEnd    = "end"
	keyPgUp   = "pgup"
	keyPgDown = "pgdown"
	keyF1     = "f1"

	// Letter keys (lowercase)
	keyJ      = "j"
	keyK      = "k"
	keyH      = "H"
	keyL      = "L"
	keyM      = "M"
	keyG      = "G"
	keyB      = "b"
	keyD      = "d"
	keyE      = "e"
	keyF      = "f"
	keyQ      = "q"
	keyR      = "r"
	keyY      = "y"
	keyN      = "n"
	keyGSmall = "g"

	// Letter keys (uppercase)
	keyYCapital = "Y"
	keyNCapital = "N"

	// Modifier keys
	keyColon = ":"

	// Shift+Letter
	keyShiftDown = "shift+down"
	keyShiftUp   = "shift+up"
	keyJCapital  = "J"
	keyKCapital  = "K"

	// Ctrl combinations
	keyCtrlC = "ctrl+c"
	keyCtrlD = "ctrl+d"
	keyCtrlG = "ctrl+g"
	keyCtrlH = "ctrl+h"
	keyCtrlL = "ctrl+l"
	keyCtrlO = "ctrl+o"
	keyCtrlP = "ctrl+p"
	keyCtrlQ = "ctrl+q"
	keyCtrlS = "ctrl+s"
	keyCtrlU = "ctrl+u"
	keyCtrlZ = "ctrl+z"

	// Command names (used with ":" prefix in input)
	cmdQuit      = "quit"
	cmdQShort    = "q"
	cmdCancel    = "cancel"
	cmdCancelAll = "cancel_all"
	cmdSuspend   = "suspend"
	cmdHelp      = "help"
)
