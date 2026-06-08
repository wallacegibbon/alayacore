package tools

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	code := m.Run()

	// Clean up temp files created by tests (truncation paths in
	// saveToTmpFile). Tests don't go through main.go's Cleanup().
	Cleanup()

	os.Exit(code)
}
