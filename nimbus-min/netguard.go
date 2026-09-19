package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AnthropicBaseURL is the default backend (and the historical only-host).
const AnthropicBaseURL = "https://api.anthropic.com"

// Network boundary, enforced in code: the ONLY dial path allows the
// active provider's host plus loopback (for local providers). There is
// no second client, no telemetry path, no update check — one transport
// constructor, everything else impossible by construction.
type guard struct {
	allowed map[string]bool // "host:port" entries
}

func guardForBase(base string) (*guard, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("network blocked: malformed base URL %q", base)
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		if strings.EqualFold(u.Scheme, "http") {
			port = "80"
		} else {
			port = "443"
		}
	}
	g := &guard{allowed: map[string]bool{strings.ToLower(host) + ":" + port: true}}
	return g, nil
}

func (g *guard) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("network blocked: malformed address %q", addr)
	}
	if !g.allowed[strings.ToLower(host)+":"+port] {
		return nil, fmt.Errorf("network blocked: Nimbus-One only contacts its configured provider (got %s)", addr)
	}
	return (&net.Dialer{Timeout: 20 * time.Second}).DialContext(ctx, network, addr)
}

// guardedHTTPClientFor returns the only HTTP client construction in the
// program, pinned to base's host.
func guardedHTTPClientFor(base string) *http.Client {
	g, err := guardForBase(base)
	if err != nil {
		// Should not happen (bases come from the validated table or an
		// explicit override); fail closed to anthropic-only.
		g, _ = guardForBase(AnthropicBaseURL)
	}
	return &http.Client{
		Timeout: 120 * time.Second,
		Transport: &http.Transport{
			DialContext:           g.dial,
			TLSHandshakeTimeout:   20 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}
}

// guardedHTTPClient is the default-backend client (kept for the
// Anthropic path and its tests).
func guardedHTTPClient() *http.Client {
	return guardedHTTPClientFor(AnthropicBaseURL)
}
