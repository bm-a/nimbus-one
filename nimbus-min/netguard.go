package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"
)

// AnthropicBaseURL is the only host Nimbus-One may contact.
const AnthropicBaseURL = "https://api.anthropic.com"

// allowedDial is the network boundary, enforced in code: only
// api.anthropic.com:443. Everything else (telemetry, updates, logging,
// analytics) is impossible by construction — there is no other dial path.
func allowedDial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("network blocked: malformed address %q", addr)
	}
	if host != "api.anthropic.com" || port != "443" {
		return nil, fmt.Errorf("network blocked: Nimbus-One only contacts api.anthropic.com:443 (got %s)", addr)
	}
	return (&net.Dialer{Timeout: 20 * time.Second}).DialContext(ctx, network, addr)
}

// guardedHTTPClient returns the only HTTP client in the program.
func guardedHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 120 * time.Second,
		Transport: &http.Transport{
			DialContext:           allowedDial,
			TLSHandshakeTimeout:   20 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}
}
