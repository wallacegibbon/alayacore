package debug

// Package debug contains a small HTTP transport wrapper that logs API
// requests and responses to a rotating local log file (or stderr as a
// fallback). It is only used when the CLI enables --debug-api or when
// providers are created with debug turned on.
//
// Each Transport carries its own io.Writer, so there is no global state.
// Tests and callers that need different output destinations can create
// independent Transports with separate writers.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// newDebugWriter picks a log destination:
//   - prefer a new file named alayacore-debug-api-N.log next to the binary;
//   - fall back to the current working directory;
//   - finally fall back to stderr if nothing works.
func newDebugWriter() io.Writer {
	// Try to create log file in executable directory.
	execPath, err := os.Executable()
	if err != nil {
		// Fallback to current directory.
		execPath = "alayacore"
	}

	execDir := filepath.Dir(execPath)
	if execDir == "." {
		if cwd, cwdErr := os.Getwd(); cwdErr == nil {
			execDir = cwd
		}
	}

	const baseName = "alayacore-debug-api"

	// Find next available log number.
	for i := range 100 {
		logName := fmt.Sprintf("%s-%d.log", baseName, i)
		logPath := filepath.Join(execDir, logName)
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644); err == nil {
			// Write the start message directly to the file, not to stderr
			fmt.Fprintf(f, "Debug log started: %s\n", filepath.Base(logPath))
			return f
		}
	}

	// Fallback to stderr if we can't create a log file.
	return os.Stderr
}

// Transport wraps an http.RoundTripper and logs requests and responses
// to its Writer. Each Transport has its own independent writer — no
// global state is shared.
type Transport struct {
	Transport http.RoundTripper
	Writer    io.Writer // where debug output is written
}

// debugReader wraps an io.Reader to log each chunk of data as it's read
type debugReader struct {
	reader    io.Reader
	writer    io.Writer
	buf       []byte
	firstRead bool
}

func newDebugReader(r io.Reader, w io.Writer) *debugReader {
	return &debugReader{
		reader:    r,
		writer:    w,
		buf:       make([]byte, 0, 4096),
		firstRead: true,
	}
}

// processContentBlock handles logging of a single content block in Anthropic streaming format
func (dr *debugReader) processContentBlock(block any) {
	blockMap, ok := block.(map[string]any)
	if !ok {
		return
	}
	blockType, _ := blockMap["type"].(string) //nolint:errcheck // debug logging, optional field
	switch blockType {
	case "tool_use":
		name, _ := blockMap["name"].(string)           //nolint:errcheck // debug logging
		input, _ := blockMap["input"].(map[string]any) //nolint:errcheck // debug logging
		inputJSON, _ := json.Marshal(input)            //nolint:errcheck // debug logging
		fmt.Fprintf(dr.writer, "{ \"content\": { type: \"tool_use\", name: %q, input: %s } }\n", name, inputJSON)
	case "thinking":
		thinking, _ := blockMap["thinking"].(string) //nolint:errcheck // debug logging
		if len(thinking) > 0 && dr.firstRead {
			fmt.Fprintf(dr.writer, "<<< Response Stream\n")
			fmt.Fprintf(dr.writer, "Chunks:\n")
			dr.firstRead = false
		}
		fmt.Fprintf(dr.writer, "{ \"content\": { type: \"thinking\", ... } }\n")
	}
}

// processJSONLine handles logging of a JSON line in SSE stream
func (dr *debugReader) processJSONLine(jsonStr string) {
	var jsonData map[string]any
	if json.Unmarshal([]byte(jsonStr), &jsonData) != nil {
		return
	}

	// Check if this is Anthropic streaming format (content as array)
	if content, ok := jsonData["content"].([]any); ok && len(content) > 0 {
		for _, block := range content {
			dr.processContentBlock(block)
		}
	}

	// Full format for final chunks or other cases
	formatted, _ := json.MarshalIndent(jsonData, "", "  ") //nolint:errcheck // debug logging
	if dr.firstRead {
		fmt.Fprintf(dr.writer, "<<< Response Stream\n")
		fmt.Fprintf(dr.writer, "Chunks:\n")
		dr.firstRead = false
	}
	fmt.Fprintf(dr.writer, "%s\n", formatted)
}

// ensureHeaderWritten writes the stream header if not already written
func (dr *debugReader) ensureHeaderWritten() {
	if dr.firstRead {
		fmt.Fprintf(dr.writer, "<<< Response Stream\n")
		fmt.Fprintf(dr.writer, "Chunks:\n")
		dr.firstRead = false
	}
}

func (dr *debugReader) Read(p []byte) (n int, err error) {
	n, err = dr.reader.Read(p)

	if n > 0 {
		chunk := p[:n]
		chunkStr := string(chunk)

		// Handle Server-Sent Events (SSE) format: "data: {...}\n"
		for line := range strings.SplitSeq(chunkStr, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			// Skip "data: " prefix and try to parse as JSON
			jsonStr := line
			if rest, found := strings.CutPrefix(line, "data:"); found {
				jsonStr = strings.TrimSpace(rest)
			}

			// Try to parse as JSON and log it
			var jsonData map[string]any
			if json.Unmarshal([]byte(jsonStr), &jsonData) == nil {
				dr.processJSONLine(jsonStr)
			} else if jsonStr != "[DONE]" {
				// Not JSON and not [DONE], print raw line
				dr.ensureHeaderWritten()
				fmt.Fprintf(dr.writer, "%s\n", line)
			}
		}
	}

	return n, err
}

// RoundTrip implements the http.RoundTripper interface
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	w := t.Writer
	var requestBody []byte
	var isStreaming bool

	// Log request and check if streaming
	if req.Body != nil {
		requestBody, _ = io.ReadAll(req.Body) //nolint:errcheck // debug logging, best effort
		req.Body = io.NopCloser(bytes.NewReader(requestBody))

		var formattedBody any
		if err := json.Unmarshal(requestBody, &formattedBody); err == nil {
			// Check if streaming is enabled
			if reqBody, ok := formattedBody.(map[string]any); ok {
				if stream, ok := reqBody["stream"].(bool); ok && stream {
					isStreaming = true
				}
			}

			formattedBody, _ = json.MarshalIndent(formattedBody, "", "  ") //nolint:errcheck // debug logging
			fmt.Fprintf(w, ">>> Request\n")
			fmt.Fprintf(w, "%s %s %s\n", req.Method, req.URL.Path, req.URL.RawQuery)
			fmt.Fprintf(w, "Headers:\n")
			for k, v := range req.Header {
				if k == "Authorization" {
					fmt.Fprintf(w, "  %s: ***\n", k)
				} else {
					fmt.Fprintf(w, "  %s: %v\n", k, v)
				}
			}
			fmt.Fprintf(w, "Body:\n")
			fmt.Fprintf(w, "%s\n", formattedBody)
		} else {
			fmt.Fprintf(w, ">>> Request\n")
			fmt.Fprintf(w, "%s %s\n", req.Method, req.URL)
			fmt.Fprintf(w, "Body:\n")
			fmt.Fprintf(w, "%s\n", string(requestBody))
		}
		fmt.Fprintf(w, "--------------------------------------------------\n")
	}

	start := time.Now()

	// Perform the request
	resp, err := t.Transport.RoundTrip(req)
	if err != nil {
		fmt.Fprintf(w, "<<< Request failed after %v: %v\n", time.Since(start), err)
		return nil, err
	}

	// Log response
	fmt.Fprintf(w, "<<< Response\n")
	fmt.Fprintf(w, "%s %s\n", resp.Proto, resp.Status)
	fmt.Fprintf(w, "Headers:\n")
	for k, v := range resp.Header {
		fmt.Fprintf(w, "  %s: %v\n", k, v)
	}

	// Check response content type to confirm streaming
	contentType := resp.Header.Get("Content-Type")
	if isStreaming && strings.Contains(contentType, "text/event-stream") {
		fmt.Fprintf(w, "Body:\n")
		resp.Body = struct {
			io.Reader
			io.Closer
		}{
			Reader: newDebugReader(resp.Body, w),
			Closer: resp.Body,
		}
	} else {
		responseBody, _ := io.ReadAll(resp.Body) //nolint:errcheck // debug logging, best effort
		resp.Body = io.NopCloser(bytes.NewReader(responseBody))

		var formattedBody any
		if err := json.Unmarshal(responseBody, &formattedBody); err == nil {
			formattedBody, _ = json.MarshalIndent(formattedBody, "", "  ") //nolint:errcheck // debug logging
			fmt.Fprintf(w, "Body:\n")
			fmt.Fprintf(w, "%s\n", formattedBody)
		} else {
			dump, _ := httputil.DumpResponse(resp, false) //nolint:errcheck // debug logging
			fmt.Fprintf(w, "Body:\n")
			fmt.Fprintf(w, "%s\n", dump)
		}
		fmt.Fprintf(w, "--------------------------------------------------\n")
		fmt.Fprintf(w, "Time: %v\n", time.Since(start))
	}

	return resp, nil
}

// NewHTTPClient creates a new HTTP client with debug logging enabled.
// Each call creates its own independent log file.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Transport: &Transport{
			Transport: http.DefaultTransport,
			Writer:    newDebugWriter(),
		},
	}
}

// NewHTTPClientWithProxy creates an HTTP client with proxy support.
// Supports HTTP, HTTPS, and SOCKS5 proxies.
func NewHTTPClientWithProxy(proxyURL string) (*http.Client, error) {
	proxyParsed, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL: %w", err)
	}

	transport := &http.Transport{}

	switch proxyParsed.Scheme {
	case "socks5", "socks5h":
		// SOCKS5 proxy
		var auth *proxy.Auth
		if proxyParsed.User != nil {
			password, _ := proxyParsed.User.Password()
			auth = &proxy.Auth{
				User:     proxyParsed.User.Username(),
				Password: password,
			}
		}
		dialer, err := proxy.SOCKS5("tcp", proxyParsed.Host, auth, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("failed to create SOCKS5 dialer: %w", err)
		}
		transport.DialContext = func(_ context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		}
	default:
		// HTTP/HTTPS proxy
		transport.Proxy = http.ProxyURL(proxyParsed)
	}

	return &http.Client{
		Transport: transport,
	}, nil
}

// NewHTTPClientWithProxyAndDebug creates an HTTP client with both proxy and debug logging.
// Each call creates its own independent log file.
func NewHTTPClientWithProxyAndDebug(proxyURL string) (*http.Client, error) {
	client, err := NewHTTPClientWithProxy(proxyURL)
	if err != nil {
		return nil, err
	}

	// Wrap the transport with debug logging
	client.Transport = &Transport{
		Transport: client.Transport,
		Writer:    newDebugWriter(),
	}

	return client, nil
}
