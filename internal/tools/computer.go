// Device computer control: screenshots and touch input.
//
// OpenClaw reference (read-only):
//
//	extensions/browser (CDP actions), plugin-sdk/browser-bridge,
//	computer-tool-node.ts (screenshots/coords). Nimbus port: no bundled
//	browser, no synthetic input daemons — screenshots and taps delegate to
//	OS helpers (Termux `screencap`/`input`, macOS `screencapture`) and
//	degrade with explicit guidance everywhere else.
package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ComputerScreenshot captures the screen to a temp PNG and returns its path.
// Termux/Android uses `screencap -p`; macOS uses `screencapture -x`.
// Other platforms (plain linux without helpers) return a descriptive error.
func ComputerScreenshot() (string, error) {
	out := filepath.Join(os.TempDir(), fmt.Sprintf("nimbus-shot-%d.png", time.Now().UnixNano()))
	var bin string
	var args []string
	switch runtime.GOOS {
	case "android":
		bin = "screencap"
		args = []string{"-p", out}
	case "darwin":
		bin = "screencapture"
		args = []string{"-x", out}
	default:
		return "", fmt.Errorf("computer: screenshots need an OS helper: "+
			"Termux (screencap), macOS (screencapture), or a rooted helper — "+
			"no screenshot backend on %s; attach an image instead", runtime.GOOS)
	}
	if _, err := exec.LookPath(bin); err != nil {
		return "", fmt.Errorf("computer: %s not found on PATH (%s): install it or attach an image instead", bin, runtime.GOOS)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	if data, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("computer: %s failed: %v (%s)", bin, err, strings.TrimSpace(string(data)))
	}
	if st, err := os.Stat(out); err != nil || st.Size() == 0 {
		return "", fmt.Errorf("computer: %s produced no output file", bin)
	}
	return out, nil
}

// ComputerTap synthesizes a tap at (x, y). Only Termux `input tap` is
// supported; every other platform returns guidance instead of a silent no-op.
func ComputerTap(x, y int) (string, error) {
	if x < 0 || y < 0 {
		return "", fmt.Errorf("computer: tap coordinates must be non-negative (got %d,%d)", x, y)
	}
	if runtime.GOOS != "android" {
		return "", fmt.Errorf("computer: tap needs Termux `input tap` on Android "+
			"(got %s) — describe the target instead", runtime.GOOS)
	}
	if _, err := exec.LookPath("input"); err != nil {
		return "", fmt.Errorf("computer: `input` not found on PATH: install Termux:API (`pkg install termux-api`) or describe the target instead")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "input", "tap", fmt.Sprint(x), fmt.Sprint(y))
	if data, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("computer: input tap failed: %v (%s)", err, strings.TrimSpace(string(data)))
	}
	return fmt.Sprintf("tapped %d,%d", x, y), nil
}

// ComputerSwipe synthesizes a swipe from (x1,y1) to (x2,y2) over ms
// milliseconds. Same platform constraints as ComputerTap.
func ComputerSwipe(x1, y1, x2, y2, ms int) (string, error) {
	for i, v := range []int{x1, y1, x2, y2} {
		if v < 0 {
			return "", fmt.Errorf("computer: swipe coordinate %d must be non-negative (got %d)", i, v)
		}
	}
	if ms <= 0 {
		ms = 300
	}
	if runtime.GOOS != "android" {
		return "", fmt.Errorf("computer: swipe needs Termux `input swipe` on Android "+
			"(got %s) — describe the gesture instead", runtime.GOOS)
	}
	if _, err := exec.LookPath("input"); err != nil {
		return "", fmt.Errorf("computer: `input` not found on PATH: install Termux:API (`pkg install termux-api`) or describe the gesture instead")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "input", "swipe",
		fmt.Sprint(x1), fmt.Sprint(y1), fmt.Sprint(x2), fmt.Sprint(y2), fmt.Sprint(ms))
	if data, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("computer: input swipe failed: %v (%s)", err, strings.TrimSpace(string(data)))
	}
	return fmt.Sprintf("swiped %d,%d -> %d,%d (%dms)", x1, y1, x2, y2, ms), nil
}

// ComputerTool exposes screenshot/tap/swipe to the agent loop.
type ComputerTool struct{}

// Name returns "computer".
func (t *ComputerTool) Name() string { return "computer" }

// Description describes the computer tool.
func (t *ComputerTool) Description() string {
	return "Device screen + touch (Termux/Android via screencap/input, macOS screenshots via screencapture; elsewhere returns guidance). Args: action (screenshot|tap|swipe), x, y, x1, y1, x2, y2, ms."
}

// Parameters describes the computer arguments.
func (t *ComputerTool) Parameters() map[string]Param {
	return map[string]Param{
		"action": {Type: "string", Description: "screenshot, tap, or swipe", Required: true},
		"x":      {Type: "number", Description: "Tap x"},
		"y":      {Type: "number", Description: "Tap y"},
		"x1":     {Type: "number", Description: "Swipe start x"},
		"y1":     {Type: "number", Description: "Swipe start y"},
		"x2":     {Type: "number", Description: "Swipe end x"},
		"y2":     {Type: "number", Description: "Swipe end y"},
		"ms":     {Type: "number", Description: "Swipe duration ms (default 300)"},
	}
}

// Execute dispatches the computer action.
func (t *ComputerTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	action := strings.ToLower(strings.TrimSpace(stringArg(args, "action")))
	num := func(key string) int {
		if v, ok := numberArg(args, key); ok {
			return int(v)
		}
		return 0
	}
	switch action {
	case "screenshot":
		return ComputerScreenshot()
	case "tap":
		return ComputerTap(num("x"), num("y"))
	case "swipe":
		return ComputerSwipe(num("x1"), num("y1"), num("x2"), num("y2"), num("ms"))
	default:
		return "", fmt.Errorf("computer: unknown action %q (want screenshot|tap|swipe)", action)
	}
}
