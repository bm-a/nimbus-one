package llm

import "testing"

func TestRouteModel(t *testing.T) {
	routes := map[string]string{"explore": "fast-m"}
	if got := RouteModel("explore", "", routes, "main"); got != "fast-m" {
		t.Fatalf("kind route = %q", got)
	}
	if got := RouteModel("Explore", "", routes, "main"); got != "fast-m" {
		t.Fatalf("kind match must be case-insensitive: %q", got)
	}
	if got := RouteModel("explore", "per-call", routes, "main"); got != "per-call" {
		t.Fatalf("explicit wins: %q", got)
	}
	if got := RouteModel("unknown", "", routes, "main"); got != "main" {
		t.Fatalf("unknown inherits primary: %q", got)
	}
	if got := RouteModel("", "", nil, "main"); got != "main" {
		t.Fatalf("empty inherits primary: %q", got)
	}
}
