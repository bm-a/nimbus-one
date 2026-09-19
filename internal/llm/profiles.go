// Package llm — model profiles (Phase 1).
//
// Different models fail at different tools and respond to different prompt
// styles. The Profile table records those quirks as DATA so the loop stays
// model-agnostic: prompt assembly picks PromptVariant, tool definitions
// pick ToolShape. Start with 2 shapes and 2 variants; new rows need
// measured justification, not vibes.
package llm

import "strings"

// Tool shapes.
const (
	// ShapeEdit exposes edit+write (exact-match tools).
	ShapeEdit = "edit"
	// ShapePatch exposes apply_patch (envelope tool) instead.
	ShapePatch = "patch"
)

// Prompt variants.
const (
	VariantDefault = "default"
	VariantGPT     = "gpt"
)

// ModelProfile adapts loop behavior to a model family.
type ModelProfile struct {
	// Family is a human label for the match rule.
	Family string
	// PromptVariant selects the system-prompt variant.
	PromptVariant string
	// ToolShape selects the edit tool surface.
	ToolShape string
	// match reports whether the profile applies to a model ID.
	match func(modelID string) bool
}

// DefaultModelProfile is the fallback: exact-match tools, default prompt.
func DefaultModelProfile() ModelProfile {
	return ModelProfile{Family: "default", PromptVariant: VariantDefault, ToolShape: ShapeEdit}
}

func gptProfile() ModelProfile {
	return ModelProfile{Family: "gpt", PromptVariant: VariantGPT, ToolShape: ShapePatch,
		match: func(id string) bool {
			l := strings.ToLower(id)
			return strings.Contains(l, "gpt-") &&
				!strings.Contains(l, "oss") &&
				!strings.Contains(l, "gpt-4")
		}}
}

// profiles lists specific families before the default.
var profiles = []ModelProfile{gptProfile()}

// MatchModelProfile returns the first matching profile, else the default.
func MatchModelProfile(modelID string) ModelProfile {
	for _, p := range profiles {
		if p.match != nil && p.match(modelID) {
			return p
		}
	}
	return DefaultModelProfile()
}

// ShapeHidesEdit reports whether the shape replaces edit/write with patch.
func ShapeHidesEdit(shape string) bool { return shape == ShapePatch }
