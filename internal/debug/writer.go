package debug

import (
	"fmt"
	"io"
	"os"
)

// NewDebugWriter creates a new debug log file with the given base name.
// It tries <baseName>-0.log, -1.log, ..., -999.log with O_EXCL so that
// concurrent processes never collide. Falls back to stderr if all slots
// are taken or the filesystem is unwritable.
func NewDebugWriter(baseName string) io.Writer {
	for i := 0; i < 1000; i++ {
		logName := fmt.Sprintf("%s-%d.log", baseName, i)
		f, err := os.OpenFile(logName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			fmt.Fprintf(f, "Debug log started: %s\n", logName)
			return f
		}
	}

	return os.Stderr
}

// CleanupDebugWriter closes and removes a debug log file created by NewDebugWriter.
// Use in tests to prevent accumulation of debug log files.
// If w is not a file (e.g. stderr fallback), it does nothing.
func CleanupDebugWriter(w io.Writer) {
	if f, ok := w.(*os.File); ok && f != os.Stderr {
		name := f.Name()
		f.Close()
		os.Remove(name)
	}
}
