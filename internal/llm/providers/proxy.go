package providers

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"

	"golang.org/x/net/proxy"
)

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
		transport.Proxy = http.ProxyURL(proxyParsed)
	}

	return &http.Client{
		Transport: transport,
	}, nil
}
