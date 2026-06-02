package debug

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestNewHTTPClient(t *testing.T) {
	client := NewHTTPClient()
	if client == nil {
		t.Fatal("NewHTTPClient returned nil")
	}

	transport, ok := client.Transport.(*Transport)
	if !ok {
		t.Fatalf("expected *Transport, got %T", client.Transport)
	}

	if transport.Writer == nil {
		t.Error("expected non-nil Writer")
	}

	if transport.Transport == nil {
		t.Error("expected non-nil underlying Transport")
	}
}

func TestNewHTTPClientWithProxy_HTTP(t *testing.T) {
	client, err := NewHTTPClientWithProxy("http://127.0.0.1:7890")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("NewHTTPClientWithProxy returned nil")
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
	}
	if client == nil {
		t.Fatal("NewHTTPClientWithProxy returned nil")
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
	}
	if client == nil {
		t.Fatal("NewHTTPClientWithProxy returned nil")
	}
}

func TestNewHTTPClientWithProxy_InvalidURL(t *testing.T) {
	_, err := NewHTTPClientWithProxy("://invalid")
	if err == nil {
		t.Fatal("expected error for invalid proxy URL, got nil")
	}
}

func TestNewHTTPClientWithProxy_EmptyURL(t *testing.T) {
	// url.Parse("") returns a URL with empty scheme, which falls through
	// to the default case (HTTP/HTTPS proxy) with the empty string as proxy.
	// This doesn't fail — it just creates a transport with an empty proxy URL,
	// which means no proxy. So we expect success, not an error.
	client, err := NewHTTPClientWithProxy("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("NewHTTPClientWithProxy returned nil")
	}
}

func TestNewHTTPClientWithProxyAndDebug(t *testing.T) {
	client, err := NewHTTPClientWithProxyAndDebug("http://127.0.0.1:7890")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("NewHTTPClientWithProxyAndDebug returned nil")
	}

	transport, ok := client.Transport.(*Transport)
	if !ok {
		t.Fatalf("expected *Transport, got %T", client.Transport)
	}

	if transport.Writer == nil {
		t.Error("expected non-nil Writer on debug transport")
	}

	// The inner transport should be an *http.Transport (with proxy configured)
	innerTransport, ok := transport.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected inner *http.Transport, got %T", transport.Transport)
	}
	if innerTransport.Proxy == nil {
		t.Error("expected Proxy func to be set on inner transport")
	}
}

func TestNewHTTPClientWithProxyAndDebug_InvalidProxy(t *testing.T) {
	_, err := NewHTTPClientWithProxyAndDebug("://invalid")
	if err == nil {
		t.Fatal("expected error for invalid proxy URL, got nil")
	}
}

func TestTransport_RoundTrip_WithoutBody(t *testing.T) {
	var logBuf strings.Builder
	transport := &Transport{
		Transport: &mockRoundTripper{},
		Writer:    &logBuf,
	}

	// Use POST with empty body so the logging path is triggered
	req, err := http.NewRequest("GET", "http://example.com/test", http.NoBody)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip failed: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	log := logBuf.String()
	if !strings.Contains(log, ">>> Request") {
		t.Error("expected request log header")
	}
	if !strings.Contains(log, "GET") {
		t.Error("expected GET method in log")
	}
	if !strings.Contains(log, "/test") {
		t.Error("expected /test path in log")
	}
}

func TestTransport_RoundTrip_WithBody(t *testing.T) {
	var logBuf strings.Builder
	transport := &Transport{
		Transport: &mockRoundTripper{},
		Writer:    &logBuf,
	}

	body := strings.NewReader(`{"key":"value"}`)
	req, err := http.NewRequest("POST", "http://example.com/api", body)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip failed: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	log := logBuf.String()
	if !strings.Contains(log, ">>> Request") {
		t.Error("expected request log header")
	}
	if !strings.Contains(log, `"key"`) {
		t.Error("expected body content in log")
	}
	if !strings.Contains(log, "<<< Response") {
		t.Error("expected response log header")
	}
}

func TestTransport_RoundTrip_AuthorizationHeader(t *testing.T) {
	var logBuf strings.Builder
	transport := &Transport{
		Transport: &mockRoundTripper{},
		Writer:    &logBuf,
	}

	// POST with body so logging fires
	req, err := http.NewRequest("POST", "http://example.com/api", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer sk-secret-key-12345")

	_, err = transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip failed: %v", err)
	}

	log := logBuf.String()
	if strings.Contains(log, "sk-secret-key-12345") {
		t.Error("Authorization header value should be redacted in log")
	}
	if !strings.Contains(log, "***") {
		t.Error("expected redacted Authorization header (***)")
	}
}

func TestTransport_RoundTrip_RequestError(t *testing.T) {
	var logBuf strings.Builder
	transport := &Transport{
		Transport: &mockRoundTripper{err: errMockFailed},
		Writer:    &logBuf,
	}

	req, err := http.NewRequest("GET", "http://example.com/api", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	_, err = transport.RoundTrip(req)
	if err == nil {
		t.Fatal("expected error from failing round tripper")
	}

	log := logBuf.String()
	if !strings.Contains(log, "Request failed") {
		t.Error("expected failure log entry, got:", log)
	}
}

// mockRoundTripper implements http.RoundTripper for testing.
type mockRoundTripper struct {
	err error
}

var errMockFailed = errors.New("mock failure")

func (m *mockRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
	}, nil
}

func TestNewDebugWriter_NotNil(t *testing.T) {
	w := newDebugWriter()
	if w == nil {
		t.Fatal("newDebugWriter returned nil")
	}
}
