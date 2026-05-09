package tools

import (
	"bufio"
	"os"
	"path/filepath"
)

// saveToTmpFile saves output to a temporary file in .alayacore.tmp/ under CWD.
// The prefix parameter controls the temp file name pattern (e.g., "cmd-*.txt").
// Using CWD (not /tmp) avoids cross-filesystem issues when the agent
// later reads the file with read_file.
func saveToTmpFile(output, prefix string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	tmpDir := filepath.Join(cwd, ".alayacore.tmp")
	if err = os.MkdirAll(tmpDir, 0755); err != nil {
		return "", err
	}

	file, err := os.CreateTemp(tmpDir, prefix)
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
