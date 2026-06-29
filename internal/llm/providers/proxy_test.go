package providers

import (
	"net/http"
	"testing"
)

func TestNewHTTPClientWithProxy_HTTP(t *testing.T) {
	client, err := NewHTTPClientWithProxy("http://127.0.0.1:7890")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
		return
	}
	if client == nil {
		t.Fatal("NewHTTPClientWithProxy returned nil")
		return
	}

	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.Transport)
	}

	if transport.Proxy == nil {
		t.Error("expected Proxy func to be set for HTTP proxy")
	}
}

func TestNewHTTPClientWithProxy_SOCKS5(t *testing.T) {
	client, err := NewHTTPClientWithProxy("socks5://127.0.0.1:1080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
		return
	}
	if client == nil {
		t.Fatal("NewHTTPClientWithProxy returned nil")
		return
	}

	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.Transport)
	}

	if transport.DialContext == nil {
		t.Error("expected DialContext to be set for SOCKS5 proxy")
	}
}

func TestNewHTTPClientWithProxy_SOCKS5WithAuth(t *testing.T) {
	client, err := NewHTTPClientWithProxy("socks5://user:pass@127.0.0.1:1080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
		return
	}
	if client == nil {
		t.Fatal("NewHTTPClientWithProxy returned nil")
		return
	}
}

func TestNewHTTPClientWithProxy_InvalidURL(t *testing.T) {
	_, err := NewHTTPClientWithProxy("://invalid")
	if err == nil {
		t.Fatal("expected error for invalid proxy URL, got nil")
	}
}

func TestNewHTTPClientWithProxy_EmptyURL(t *testing.T) {
	client, err := NewHTTPClientWithProxy("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("NewHTTPClientWithProxy returned nil")
	}
}
