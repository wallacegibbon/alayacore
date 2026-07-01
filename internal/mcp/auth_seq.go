package mcp

import (
	"context"
	"sync/atomic"
	"time"
)

// ServerOAuthResult holds the outcome of a single server's OAuth flow.
type ServerOAuthResult struct {
	Name  string
	Tools []Tool
	Err   error
}

// OAuthSeq manages the OAuth authorization sequence for a set of MCP servers.
// It owns the per-server state (ServerAuth instances) and provides a simple
// API for the session to drive the flow:
//
//  1. NextConfirm() returns the next server needing user confirmation
//  2. Start(ctx, name) launches OAuth in a background goroutine
//  3. TryResult() collects completed results (non-blocking)
//  4. Done() is true when all servers have been processed
//
// All methods are safe to call from the session's run() goroutine.
type OAuthSeq struct {
	auths   []*ServerAuth
	results chan ServerOAuthResult

	// started tracks servers the user confirmed (OAuth goroutine launched).
	// skipped tracks servers the user declined.
	// Both are map[string]bool guarded by the mutex, but the hot path
	// for started also uses an atomic counter for RunningCount.
	started map[string]bool
	skipped map[string]bool
	running atomic.Int32 // number of goroutines in flight
}

// NewOAuthSeq creates an OAuthSeq from clients that need authorization.
// Clients that already have a valid token are excluded.
// Returns nil if no clients need authorization.
func NewOAuthSeq(clients []*Client) *OAuthSeq {
	var auths []*ServerAuth
	for _, c := range clients {
		if c.needsPersistedAuth() {
			auths = append(auths, NewServerAuth(c))
		}
	}
	if len(auths) == 0 {
		return nil
	}
	return &OAuthSeq{
		auths:   auths,
		results: make(chan ServerOAuthResult, len(auths)),
		started: make(map[string]bool),
		skipped: make(map[string]bool),
	}
}

// NextConfirm returns the next server that needs user confirmation,
// or nil if all servers have been prompted.
// Servers are returned in config order, one at a time.
func (s *OAuthSeq) NextConfirm() *ServerAuth {
	for _, a := range s.auths {
		if !s.started[a.Name()] && !s.skipped[a.Name()] {
			return a
		}
	}
	return nil
}

// Start begins OAuth for the named server in a background goroutine.
// Returns false if the server was already started or skipped.
// Non-blocking: the goroutine sends its result through the results channel.
// Each goroutine gets a 5-minute timeout context (plenty for interactive OAuth).
func (s *OAuthSeq) Start(name string) bool {
	if s.started[name] || s.skipped[name] {
		return false
	}

	var auth *ServerAuth
	for _, a := range s.auths {
		if a.Name() == name {
			auth = a
			break
		}
	}
	if auth == nil {
		return false
	}

	s.started[name] = true
	s.running.Add(1)

	go func() {
		// Each OAuth flow gets its own 5-minute timeout context.
		// The context is independent per goroutine, so parallel
		// executions don't interfere.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		tools, err := auth.Run(ctx)
		s.results <- ServerOAuthResult{Name: name, Tools: tools, Err: err}
		s.running.Add(-1)
	}()

	return true
}

// Skip marks a server as skipped (user declined authorization).
// Does not start any goroutine.
func (s *OAuthSeq) Skip(name string) {
	s.skipped[name] = true
}

// TryResult returns a completed OAuth result, or nil if none available.
// Non-blocking.
func (s *OAuthSeq) TryResult() *ServerOAuthResult {
	select {
	case r := <-s.results:
		return &r
	default:
		return nil
	}
}

// PendingCount returns the number of servers still waiting for user input.
func (s *OAuthSeq) PendingCount() int {
	count := 0
	for _, a := range s.auths {
		if !s.started[a.Name()] && !s.skipped[a.Name()] {
			count++
		}
	}
	return count
}

// RunningCount returns the number of servers currently running OAuth.
func (s *OAuthSeq) RunningCount() int {
	return int(s.running.Load())
}

// Done returns true when all servers have been either started (and
// completed) or skipped — no pending, no running goroutines.
func (s *OAuthSeq) Done() bool {
	return s.PendingCount() == 0 && s.RunningCount() == 0
}
