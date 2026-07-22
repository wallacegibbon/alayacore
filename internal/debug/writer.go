package debug

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// NewDebugWriter creates a new debug log file in the given directory.
// It tries <baseName>-0.log, -1.log, ..., -999.log with O_EXCL so that
// concurrent processes never collide.
// Falls back to stderr if all slots are taken or the filesystem is unwritable.
func NewDebugWriter(dir, baseName string) io.WriteCloser {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return os.Stderr
	}

	for i := 0; i < 1000; i++ {
		logName := filepath.Join(dir, fmt.Sprintf("%s-%d.log", baseName, i))
		f, err := os.OpenFile(logName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			fmt.Fprintf(f, "Debug log started: %s\n", logName)
			return f
		}
	}

	return os.Stderr // *os.File implements io.WriteCloser
}
