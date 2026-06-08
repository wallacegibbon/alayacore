package tools

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain runs all tests in the package from a temporary directory.
// This prevents test artifacts (like .alayacore-tmp-* directories created by
// truncation path in saveToTmpFile) from leaking into the source tree.
// The temp directory is created on the same filesystem as the package
// source (using the parent directory) to avoid cross-filesystem issues.
// The entire temp directory is removed after all tests complete.
func TestMain(m *testing.M) {
	cwd, err := os.Getwd()
	if err != nil {
		os.Exit(1)
	}

	tmpDir, err := os.MkdirTemp(filepath.Dir(cwd), "alayacore-tools-test-*")
	if err != nil {
		os.Exit(1)
	}

	if err := os.Chdir(tmpDir); err != nil {
		os.RemoveAll(tmpDir)
		os.Exit(1)
	}

	code := m.Run()

	os.Chdir(cwd)
	os.RemoveAll(tmpDir)
	os.Exit(code)
}
