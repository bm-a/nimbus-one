package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// IsTermux reports whether we run inside Termux (home prefix /data/data/com.termux).
func IsTermux() bool {
	if home, err := os.UserHomeDir(); err == nil {
		if strings.HasPrefix(home, "/data/data/com.termux") {
			return true
		}
	}
	if _, err := os.Stat("/data/data/com.termux/files/usr/bin"); err == nil {
		return true
	}
	return false
}

// WakeLock acquires a Termux wake lock. No-op (nil) when the helper is absent.
func WakeLock() error {
	path, err := exec.LookPath("termux-wake-lock")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, path).Run(); err != nil {
		return fmt.Errorf("termux-wake-lock: %w", err)
	}
	return nil
}

// WakeUnlock releases a Termux wake lock. No-op (nil) when helper is absent.
func WakeUnlock() error {
	path, err := exec.LookPath("termux-wake-unlock")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, path).Run(); err != nil {
		return fmt.Errorf("termux-wake-unlock: %w", err)
	}
	return nil
}

// BatteryPct returns the battery percentage via termux-battery-status.
// Errors when the helper is missing or output cannot be parsed.
func BatteryPct(ctx context.Context) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	path, err := exec.LookPath("termux-battery-status")
	if err != nil {
		return 0, fmt.Errorf("termux-battery-status not found: %w", err)
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(callCtx, path).Output()
	if err != nil {
		return 0, fmt.Errorf("termux-battery-status: %w", err)
	}
	var payload struct {
		Percentage int `json:"percentage"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return 0, fmt.Errorf("parse battery status: %w", err)
	}
	return payload.Percentage, nil
}

// ThrottleForBattery scales base interval under low battery:
// pct < 10 -> 6x base; pct < 20 -> 3x base; otherwise base.
func ThrottleForBattery(pct int, base time.Duration) time.Duration {
	switch {
	case pct < 10:
		return 6 * base
	case pct < 20:
		return 3 * base
	default:
		return base
	}
}
