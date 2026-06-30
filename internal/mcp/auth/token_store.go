package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TokenStore defines the interface for persisting and loading OAuth tokens.
// Implementations must be safe for concurrent use.
type TokenStore interface {
	// LoadToken returns a previously saved token for the given server ID,
	// or nil if no token exists.
	LoadToken(serverID string) (*Token, error)

	// SaveToken persists a token for the given server ID.
	SaveToken(serverID string, token *Token) error

	// DeleteToken removes a persisted token for the given server ID.
	DeleteToken(serverID string) error
}

// FileTokenStore persists OAuth tokens as JSON files on disk.
// Tokens are stored in a single directory, one file per server.
// The directory is created on first use if it does not exist.
type FileTokenStore struct {
	dir string
	mu  sync.RWMutex
}

// NewFileTokenStore creates a FileTokenStore rooted at dir.
// The directory will be created on the first write if it does not exist.
func NewFileTokenStore(dir string) *FileTokenStore {
	return &FileTokenStore{dir: dir}
}

// DefaultTokenDir returns the default token storage directory
// under the user's alayacore config directory.
func DefaultTokenDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, ".alayacore", "mcp-tokens"), nil
}

// tokenFileName returns the file path for a given server ID.
// The server ID is sanitized to prevent directory traversal.
func (s *FileTokenStore) tokenFileName(serverID string) string {
	// Sanitize: replace any path separators with underscore
	safeName := sanitizeFileTokenName(serverID)
	return filepath.Join(s.dir, safeName+".json")
}

// sanitizeFileTokenName replaces characters problematic for file names.
func sanitizeFileTokenName(name string) string {
	// Replace path separators and null bytes, keep most other chars
	result := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c == '/' || c == '\\' || c == '\x00':
			result = append(result, '_')
		case c == '.' || c == '-' || c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'):
			result = append(result, c)
		default:
			// Encode other characters as hex to avoid file system issues
			result = append(result, fmt.Sprintf("_%02x", c)...)
		}
	}
	return string(result)
}

// tokenFilePayload is the on-disk structure for a persisted token.
// It mirrors Token but ensures JSON serialization is explicit.
type tokenFilePayload struct {
	AccessToken  string   `json:"access_token"`
	TokenType    string   `json:"token_type"`
	RefreshToken string   `json:"refresh_token,omitempty"`
	ExpiresAt    int64    `json:"expires_at,omitempty"` // Unix timestamp (seconds), 0 = no expiration
	Scopes       []string `json:"scopes,omitempty"`

	// Refresh metadata (saved alongside token for automatic refresh).
	TokenEndpoint string `json:"token_endpoint,omitempty"`
	ClientID      string `json:"client_id,omitempty"`
}

// LoadToken loads a persisted token for the given server ID.
// Returns nil if no token file exists.
func (s *FileTokenStore) LoadToken(serverID string) (*Token, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path := s.tokenFileName(serverID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read token file %s: %w", path, err)
	}

	var payload tokenFilePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parse token file %s: %w", path, err)
	}

	token := &Token{
		AccessToken:   payload.AccessToken,
		TokenType:     payload.TokenType,
		RefreshToken:  payload.RefreshToken,
		Scopes:        payload.Scopes,
		TokenEndpoint: payload.TokenEndpoint,
		ClientID:      payload.ClientID,
	}
	if payload.ExpiresAt > 0 {
		token.ExpiresAt = unixTime(payload.ExpiresAt)
	}
	// If ExpiresAt is 0, it stays as zero value → Valid() treats it as non-expiring.

	return token, nil
}

// SaveToken persists a token for the given server ID.
func (s *FileTokenStore) SaveToken(serverID string, token *Token) error {
	if token == nil {
		return s.DeleteToken(serverID)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Ensure directory exists.
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return fmt.Errorf("create token dir %s: %w", s.dir, err)
	}

	payload := tokenFilePayload{
		AccessToken:   token.AccessToken,
		TokenType:     token.TokenType,
		RefreshToken:  token.RefreshToken,
		Scopes:        token.Scopes,
		TokenEndpoint: token.TokenEndpoint,
		ClientID:      token.ClientID,
	}
	if !token.ExpiresAt.IsZero() {
		payload.ExpiresAt = token.ExpiresAt.Unix()
	}
	// If ExpiresAt is zero (no expiration), omit the field in JSON.

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal token: %w", err)
	}

	path := s.tokenFileName(serverID)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write token file %s: %w", path, err)
	}

	return nil
}

// DeleteToken removes a persisted token file for the given server ID.
func (s *FileTokenStore) DeleteToken(serverID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.tokenFileName(serverID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete token file %s: %w", path, err)
	}
	return nil
}

// unixTime converts a Unix timestamp (seconds) to time.Time.
func unixTime(sec int64) time.Time {
	return time.Unix(sec, 0)
}
