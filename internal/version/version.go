// Package version provides the application version string.
// It is a separate package so that all other packages can import it
// without introducing circular dependencies.
package version

// Version is the current application version.
// Hard-coded here as a default; overridden at build time via:
//
//	go build -ldflags "-X github.com/alayacore/alayacore/internal/version.Version=$(git describe --tags --always)"
//
// Must be a var (not const) so the linker can patch it.
var Version = "(devel)"
