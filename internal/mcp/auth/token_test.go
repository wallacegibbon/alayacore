package auth

import (
	"context"
	"testing"
	"time"
)

func TestToken_Valid(t *testing.T) {
	tests := []struct {
		name  string
		token *Token
		want  bool
	}{
		{"nil token", nil, false},
		{"empty token", &Token{}, false},
		{"expired", &Token{AccessToken: "abc", ExpiresAt: time.Now().Add(-1 * time.Hour)}, false},
		{"valid", &Token{AccessToken: "abc", ExpiresAt: time.Now().Add(1 * time.Hour)}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.token.Valid(); got != tt.want {
				t.Errorf("Token.Valid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStaticProvider(t *testing.T) {
	t.Run("returns token", func(t *testing.T) {
		p := &StaticProvider{
			TokenValue: &Token{
				AccessToken: "test-token",
				TokenType:   "Bearer",
				ExpiresAt:   time.Now().Add(1 * time.Hour),
			},
		}
		tok, err := p.Token(context.Background())
		if err != nil {
			t.Fatalf("Token() error = %v", err)
		}
		if tok.AccessToken != "test-token" {
			t.Errorf("got %q, want %q", tok.AccessToken, "test-token")
		}
	})

	t.Run("nil token returns nil", func(t *testing.T) {
		p := &StaticProvider{}
		tok, err := p.Token(context.Background())
		if err != nil {
			t.Fatalf("Token() error = %v", err)
		}
		if tok != nil {
			t.Errorf("expected nil, got %v", tok)
		}
	})
}

func TestCachedProvider(t *testing.T) {
	calls := 0
	inner := &callCountingProvider{fn: func() (*Token, error) {
		calls++
		return &Token{
			AccessToken: "tok",
			TokenType:   "Bearer",
			ExpiresAt:   time.Now().Add(1 * time.Hour),
		}, nil
	}}

	p := NewCached(inner)

	// First call should hit the inner provider.
	tok1, err := p.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}

	// Second call should use cache.
	tok2, err := p.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("expected still 1 call after cache hit, got %d", calls)
	}

	if tok1.AccessToken != tok2.AccessToken {
		t.Error("cached token mismatch")
	}
}

func TestCachedProvider_Expired(t *testing.T) {
	calls := 0
	inner := &callCountingProvider{fn: func() (*Token, error) {
		calls++
		return &Token{
			AccessToken: "tok",
			TokenType:   "Bearer",
			ExpiresAt:   time.Now().Add(1 * time.Hour),
		}, nil
	}}

	p := NewCached(inner)

	// First call.
	_, _ = p.Token(context.Background())

	// Force expiry and call again.
	p.(*cachedProvider).token = &Token{
		AccessToken: "expired",
		ExpiresAt:   time.Now().Add(-1 * time.Hour),
	}

	tok, err := p.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls (expired caused refresh), got %d", calls)
	}
	if tok.AccessToken != "tok" {
		t.Errorf("expected fresh token, got %q", tok.AccessToken)
	}
}

// callCountingProvider is a TokenProvider that counts calls.
type callCountingProvider struct {
	fn func() (*Token, error)
}

func (p *callCountingProvider) Token(_ context.Context) (*Token, error) {
	return p.fn()
}
