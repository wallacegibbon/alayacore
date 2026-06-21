package tools

import (
	"bufio"
	"os"
	"sync"
)

var (
	procTmpDir          string
	procTmpDirOnce      sync.Once
	procTmpDirCreated   bool // true when MkdirTemp succeeded; prevents Cleanup from removing the system temp root
)

// procTmpDirInit creates a per-process temporary directory under the
// system temp directory (os.TempDir()). Each process gets its own
// uniquely-named directory so concurrently running alayacore instances
// never collide. Uses os.MkdirTemp for atomic, collision-free creation.
func procTmpDirInit() {
	var err error
	procTmpDir, err = os.MkdirTemp(os.TempDir(), "alayacore-*")
	if err != nil {
		// Fall back to the system temp root if we can't create the scoped dir.
		procTmpDir = os.TempDir()
		return
	}
	procTmpDirCreated = true
}

// saveToTmpFile saves output to a temporary file in this process's
// directory under the system temp directory (e.g. /tmp/alayacore-1234567890/).
// The returned absolute path can be read back with read_file.
func saveToTmpFile(output, prefix string) (string, error) {
	procTmpDirOnce.Do(procTmpDirInit)

	file, err := os.CreateTemp(procTmpDir, prefix)
	if err != nil {
		return "", err
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	if _, err = writer.WriteString(output); err != nil {
		return "", err
	}

	if err = writer.Flush(); err != nil {
		return "", err
	}

	return file.Name(), nil
}

// Cleanup removes this process's temporary directory.
// Safe to call even when the scoped directory could not be created
// (only the scoped dir created by MkdirTemp is removed, never the
// system temp root).
func Cleanup() {
	if procTmpDirCreated && procTmpDir != "" {
		os.RemoveAll(procTmpDir)
	}
}
