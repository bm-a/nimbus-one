// Package nettail integrates the local Tailscale CLI for tailnet exposure
// and tracks paired remote nodes.
//
// OpenClaw reference (read-only):
//   - src/gateway/server-tailscale.ts (GatewayTailscaleMode off|serve|funnel,
//     claim hostname, funnel publish, warn-and-continue on failure)
//   - src/infra/tailscale.ts (claimTailscaleRoute, serve/funnel route owner,
//     serve status, tailnet hostname from status JSON)
//   - src/gateway/node-registry.ts + node-pairing-*.ts + node-command-policy
//     (node capabilities/command policy, pairing surface)
//
// Design notes: every tailscale operation returns a descriptive error and
// never exits the process; callers log a warning and continue degraded,
// mirroring OpenClaw's warn-and-continue exposure helper.
//
// Exact CLI behavior used here (flags vary across Tailscale CLI versions):
// as captured in OpenClaw's src/infra/tailscale.ts, the route is claimed by
// a foreground owner process:
//
//	tailscale <serve|funnel> --yes --bg=false <port>
//
// Route inspection via `tailscale serve status --json` and identity via
// `tailscale status --json`. We probe `tailscale version` before claiming so
// a missing/broken CLI surfaces as a descriptive error instead of a hang.
package nettail

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Mode mirrors OpenClaw's GatewayTailscaleMode: off|serve|funnel.
type Mode string

const (
	ModeOff    Mode = "off"
	ModeServe  Mode = "serve"
	ModeFunnel Mode = "funnel"
)

// ParseMode parses a tailscale exposure mode string.
func ParseMode(s string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(s))) {
	case ModeOff:
		return ModeOff, nil
	case ModeServe:
		return ModeServe, nil
	case ModeFunnel:
		return ModeFunnel, nil
	default:
		return "", fmt.Errorf("nettail: unknown tailscale mode %q (want off|serve|funnel)", s)
	}
}

// tailscaleBinary is the CLI name/override used for every invocation.
// Tests in this package may override it; production code leaves it as-is.
var tailscaleBinary = "tailscale"

// lookPath is a variable so tests can observe lookup behavior without a daemon.
var lookPath = exec.LookPath

// Available reports whether the tailscale CLI is present in PATH.
// It makes no daemon assumption: true only means the binary resolves.
func Available() bool {
	_, err := lookPath(tailscaleBinary)
	return err == nil
}

// BuildServeArgs returns the exact argv (minus binary) used to claim a
// tailnet serve route for port, mirroring OpenClaw's foreground route owner.
func BuildServeArgs(port int) []string {
	return []string{"serve", "--yes", "--bg=false", strconv.Itoa(port)}
}

// BuildFunnelArgs returns the exact argv (minus binary) used to claim a
// tailnet funnel route for port.
func BuildFunnelArgs(port int) []string {
	return []string{"funnel", "--yes", "--bg=false", strconv.Itoa(port)}
}

// findBinary resolves the tailscale CLI or returns a descriptive error.
func findBinary() (string, error) {
	p, err := lookPath(tailscaleBinary)
	if err != nil {
		return "", fmt.Errorf("nettail: tailscale binary %q not found in PATH: %w", tailscaleBinary, err)
	}
	return p, nil
}

// probeVersion runs `tailscale version` to fail fast with a descriptive
// error when the CLI is present but unusable.
func probeVersion(ctx context.Context, bin string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, "version")
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("nettail: tailscale version probe failed (%s): %w (output: %s)",
			bin, err, truncate(out.String(), 500))
	}
	return nil
}

func checkPort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("nettail: invalid port %d (want 1-65535)", port)
	}
	return nil
}

// runRoute claims a serve/funnel route as a foreground child process bound
// to ctx. It blocks until ctx is cancelled (returning nil) or the CLI exits
// with an error. Failure is never fatal: the caller logs the returned error
// as a warning and continues without tailnet exposure.
func runRoute(ctx context.Context, mode string, port int, args []string) error {
	if err := checkPort(port); err != nil {
		return err
	}
	bin, err := findBinary()
	if err != nil {
		return err
	}
	if err := probeVersion(ctx, bin); err != nil {
		return err
	}
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			// Cancelled by the owner: clean shutdown, not a failure.
			return nil
		}
		return fmt.Errorf("nettail: tailscale %s failed: %w (output: %s)",
			mode, err, truncate(out.String(), 1000))
	}
	return nil
}

// Serve claims a tailnet serve route for port and blocks until ctx is done.
// See runRoute for failure semantics (descriptive error, never fatal).
func Serve(ctx context.Context, port int) error {
	if err := checkPort(port); err != nil {
		return err
	}
	return runRoute(ctx, "serve", port, BuildServeArgs(port))
}

// Funnel claims a tailnet funnel route for port and blocks until ctx is done.
// See runRoute for failure semantics (descriptive error, never fatal).
func Funnel(ctx context.Context, port int) error {
	if err := checkPort(port); err != nil {
		return err
	}
	return runRoute(ctx, "funnel", port, BuildFunnelArgs(port))
}

// Status runs `tailscale status --json` and returns the raw JSON stdout.
func Status(ctx context.Context) (string, error) {
	bin, err := findBinary()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, "status", "--json")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("nettail: tailscale status timed out: %w", ctx.Err())
		}
		return "", fmt.Errorf("nettail: tailscale status failed: %w (stderr: %s)",
			err, truncate(stderr.String(), 500))
	}
	return stdout.String(), nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
