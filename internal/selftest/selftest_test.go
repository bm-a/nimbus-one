package selftest

import (
	"context"
	"testing"
	"time"
)

// TestAllScenarios runs the full fault matrix inside go test.
// Hermetic: httptest + temp dirs + stub providers only.
func TestAllScenarios(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	fails := 0
	for _, r := range Run(ctx) {
		if !r.Pass {
			t.Errorf("[%s] %s: %s", r.ID, r.Title, r.Detail)
			fails++
		}
	}
	if fails > 0 {
		t.Fatalf("%d scenarios failed", fails)
	}
}
