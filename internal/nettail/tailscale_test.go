package nettail

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestAvailableBoolOnly(t *testing.T) {
	// No daemon assumption: Available only reports PATH presence as a bool.
	_ = Available()
}

func TestBuildServeArgsShape(t *testing.T) {
	args := BuildServeArgs(8080)
	if len(args) == 0 || args[0] != "serve" {
		t.Fatalf("BuildServeArgs = %q, want leading \"serve\"", args)
	}
	if args[len(args)-1] != "8080" {
		t.Fatalf("BuildServeArgs = %q, want trailing port", args)
	}
	if !reflect.DeepEqual(args, []string{"serve", "--yes", "--bg=false", "8080"}) {
		t.Fatalf("BuildServeArgs = %q, unexpected shape", args)
	}
}

func TestBuildFunnelArgsShape(t *testing.T) {
	args := BuildFunnelArgs(443)
	if len(args) == 0 || args[0] != "funnel" {
		t.Fatalf("BuildFunnelArgs = %q, want leading \"funnel\"", args)
	}
	if args[len(args)-1] != "443" {
		t.Fatalf("BuildFunnelArgs = %q, want trailing port", args)
	}
	if !reflect.DeepEqual(args, []string{"funnel", "--yes", "--bg=false", "443"}) {
		t.Fatalf("BuildFunnelArgs = %q, unexpected shape", args)
	}
}

func withBadBinary(t *testing.T) {
	t.Helper()
	old := tailscaleBinary
	tailscaleBinary = "definitely-not-a-tailscale-binary-xyz"
	t.Cleanup(func() { tailscaleBinary = old })
}

func TestFailureDescriptive(t *testing.T) {
	withBadBinary(t)
	ctx := context.Background()
	if err := Serve(ctx, 8080); err == nil || !strings.Contains(err.Error(), "tailscale") {
		t.Fatalf("Serve with bad binary: err=%v, want error mentioning tailscale", err)
	}
	if err := Funnel(ctx, 8080); err == nil || !strings.Contains(err.Error(), "tailscale") {
		t.Fatalf("Funnel with bad binary: err=%v, want error mentioning tailscale", err)
	}
	if _, err := Status(ctx); err == nil || !strings.Contains(err.Error(), "tailscale") {
		t.Fatalf("Status with bad binary: err=%v, want error mentioning tailscale", err)
	}
}

func TestBadPort(t *testing.T) {
	ctx := context.Background()
	for _, port := range []int{0, -1, 70000} {
		if err := Serve(ctx, port); err == nil {
			t.Fatalf("Serve(%d) = nil, want error", port)
		}
		if err := Funnel(ctx, port); err == nil {
			t.Fatalf("Funnel(%d) = nil, want error", port)
		}
	}
}

func TestParseMode(t *testing.T) {
	for in, want := range map[string]Mode{"off": ModeOff, "SERVE": ModeServe, "funnel": ModeFunnel} {
		got, err := ParseMode(in)
		if err != nil || got != want {
			t.Fatalf("ParseMode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseMode("bogus"); err == nil {
		t.Fatalf("ParseMode(bogus) = nil, want error")
	}
}
