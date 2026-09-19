package tools

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

func TestComputerToolUnknownAction(t *testing.T) {
	tool := &ComputerTool{}
	if tool.Name() != "computer" {
		t.Fatalf("Name = %q", tool.Name())
	}
	if _, err := tool.Execute(context.Background(), map[string]any{"action": "dance"}); err == nil {
		t.Fatal("unknown action must error")
	}
}

func TestComputerTapNegativeCoords(t *testing.T) {
	if _, err := ComputerTap(-1, 5); err == nil || !strings.Contains(err.Error(), "non-negative") {
		t.Fatalf("negative tap must error, got %v", err)
	}
	if _, err := ComputerSwipe(0, 0, -3, 4, 100); err == nil || !strings.Contains(err.Error(), "non-negative") {
		t.Fatalf("negative swipe must error, got %v", err)
	}
}

func TestComputerMissingHelperGuidance(t *testing.T) {
	// Empty PATH forces LookPath failures on every platform.
	t.Setenv("PATH", t.TempDir())
	if _, err := ComputerTap(10, 10); err == nil {
		t.Fatal("tap must error without helper")
	} else if runtime.GOOS == "android" {
		if !strings.Contains(err.Error(), "`input` not found") {
			t.Fatalf("android tap must name the missing helper: %v", err)
		}
	} else if !strings.Contains(err.Error(), "needs Termux") {
		t.Fatalf("non-android tap must explain platform gap: %v", err)
	}
	if _, err := ComputerSwipe(0, 0, 5, 5, 0); err == nil {
		t.Fatal("swipe must error without helper")
	}
	if _, err := ComputerScreenshot(); err == nil {
		t.Fatal("screenshot must error without helper")
	} else if runtime.GOOS != "android" && runtime.GOOS != "darwin" {
		if !strings.Contains(err.Error(), "no screenshot backend") {
			t.Fatalf("linux screenshot must describe the gap: %v", err)
		}
	}
}

func TestComputerToolDispatchValidation(t *testing.T) {
	tool := &ComputerTool{}
	// Negative coords fail before any platform/helper check.
	if _, err := tool.Execute(context.Background(), map[string]any{"action": "tap", "x": -2, "y": 3}); err == nil {
		t.Fatal("negative tap via tool must error")
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := tool.Execute(context.Background(), map[string]any{"action": "screenshot"}); err == nil {
		t.Fatal("screenshot without helper must error, never silently succeed")
	}
}
