// Package app provides shared initialization and the Adapter interface
// for all UI frontends (terminal, plainio, rawio).
package app

// Adapter is the interface for all UI adapters (terminal, plainio, etc.).
type Adapter interface {
	Start() int
}
