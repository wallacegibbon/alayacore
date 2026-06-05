// Package plainio provides a plain stdin/stdout adapter for AlayaCore.
//
// It reads user prompts from stdin (one per newline) and prints assistant
// messages to stdout. No terminal features (ANSI codes, TTY detection, etc.)
// are used — just plain IO.
//
// Activate with the --plainio flag.
//
// Input rules:
//   - Each line is treated as a separate prompt.
//   - A trailing backslash (\) before newline continues the prompt on the next line.
//   - Ctrl-D (EOF) closes input; the program exits after the current task finishes (code 0).
//   - Ctrl-C (SIGINT): terminates immediately with default signal handling
//     (exit code 130).
//   - Errors during the session cause input to close. The program waits for the
//     current task to finish, then exits with code 1. Remaining queued tasks
//     are NOT executed.
//   - A clean exit (EOF with no errors) returns code 0.
//
// Output format:
//   - Assistant text/reasoning: printed directly (stream ID prefix stripped).
//     A blank line is inserted when consecutive deltas belong to different
//     stream groups or different message types.
//   - User prompts: prefixed with "> ".
//   - Tool calls: start frames show the tool name; input frames show the arguments.
//   - Tool results: suppressed.
//   - Errors: prefixed with "Error: ".
//   - Notifications: prefixed with "[...]".
//   - Tool confirmations: shown as "[tool_confirm: allow tool "id" to run?]".
//   - A blank line is printed after each task completes.
//
// Communication with the session layer uses the same TLV protocol as the
// terminal and plainio adapters.
//
// Key Files:
//   - adapter.go: Adapter struct, Start() entry point
//   - input.go: Stdin line reader with backslash continuation
//   - output.go: TLV parser and plain-text renderer
package plainio
