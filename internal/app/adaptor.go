package app

// Adaptor is the interface for all UI adaptors (terminal, plainio, etc.).
type Adaptor interface {
	Start() int
}
