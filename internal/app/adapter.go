package app

// Adapter is the interface for all UI adapters (terminal, plainio, etc.).
type Adapter interface {
	Start() int
}
