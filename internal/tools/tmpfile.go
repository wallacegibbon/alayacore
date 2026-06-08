package tools

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var (
	procTmpDir     string
	procTmpDirOnce sync.Once
)

// procTmpDirInit creates a per-process temporary directory.
// Each alayacore process gets its own uniquely-named directory under CWD
// so there is no risk of interfering with other concurrently running
// instances — the directory name itself is unique per process.
func procTmpDirInit() {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	// Generate a random 8-byte suffix using crypto/rand for uniqueness.
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		suffix = [8]byte{} // zero — unlikely but fallback
	}
	procTmpDir = filepath.Join(cwd, fmt.Sprintf(".alayacore-tmp-%d-%s", os.Getpid(), hex.EncodeToString(suffix[:])))

	if err := os.MkdirAll(procTmpDir, 0755); err != nil {
		// Fall back to the base CWD if we can't create the scoped dir.
		// Without this, saveToTmpFile would fail on every large output.
		procTmpDir = cwd
	}
}

// saveToTmpFile saves output to a temporary file in this process's
// directory (e.g. .alayacore-tmp-28980-3b5ffe86220d3d7a/).
// Using CWD (not /tmp) avoids cross-filesystem issues when the agent
// later reads the file with read_file.
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
// Safe to call from main.go — only affects this process's files.
func Cleanup() {
	d := procTmpDir
	if d != "" {
		os.RemoveAll(d)
	}
}
