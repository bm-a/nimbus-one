// Full OpenRouter support: model catalog, live key validation, and the
// curated free-tier-first fallback chain. OpenRouter is the recommended
// default because one key unlocks hundreds of models (including free tiers)
// through a single OpenAI-compatible endpoint — ideal for phones.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenRouterModelsURL lists the catalog; OpenRouterBaseURL is the chat endpoint.
const OpenRouterModelsURL = "https://openrouter.ai/api/v1/models"

// SuggestedModels is a recommendation list only — Meta Muse Spark first.
// It is NEVER applied automatically: fallbacks come solely from user
// configuration (config file `fallback_models`, NIMBUS_FALLBACK_MODELS, or
// the `nimbus-one config` wizard where the user confirms the order).
// Deterministic order, no AI routing.
var SuggestedModels = []string{
	"meta/muse-spark-1.3-contributor",
	"openrouter/auto",
	"anthropic/claude-sonnet-4",
	"openai/gpt-4o-mini",
}

// OpenRouterModel is one catalog entry.
type OpenRouterModel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ListOpenRouterModels fetches the public catalog (no key needed).
// On any failure it returns SuggestedModels-derived entries so callers always
// have something usable offline.
func ListOpenRouterModels(ctx context.Context) ([]OpenRouterModel, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, OpenRouterModelsURL, nil)
	if err != nil {
		return fallbackModels(), err
	}
	req.Header.Set("User-Agent", "Nimbus-One/1.0")
	hc := &http.Client{Timeout: 20 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return fallbackModels(), err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fallbackModels(), fmt.Errorf("openrouter models: status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fallbackModels(), err
	}
	var out struct {
		Data []OpenRouterModel `json:"data"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return fallbackModels(), err
	}
	if len(out.Data) == 0 {
		return fallbackModels(), fmt.Errorf("openrouter models: empty catalog")
	}
	return out.Data, nil
}

func fallbackModels() []OpenRouterModel {
	out := make([]OpenRouterModel, 0, len(SuggestedModels))
	for _, id := range SuggestedModels {
		out = append(out, OpenRouterModel{ID: id, Name: id})
	}
	return out
}

// PingOpenRouter validates a key live against GET /v1/models with Bearer
// auth and returns the preferred usable model ID. Used by `nimbus-one config`
// and the TUI wizard for inline validation.
func PingOpenRouter(ctx context.Context, apiKey string) (string, error) {
	return pingOpenRouterURL(ctx, apiKey, OpenRouterModelsURL)
}

func pingOpenRouterURL(ctx context.Context, apiKey, url string) (string, error) {
	if strings.TrimSpace(apiKey) == "" {
		return "", fmt.Errorf("empty API key")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", "Nimbus-One/1.0")
	hc := &http.Client{Timeout: 20 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return "", fmt.Errorf("key rejected (status %d) — check the key, no retry", resp.StatusCode)
	}
	if resp.StatusCode == 429 {
		// Key is valid but rate-limited: still a pass for validation.
		return SuggestedModels[0], nil
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return SuggestedModels[0], nil // valid key, unreadable body — pass with default
	}
	var out struct {
		Data []OpenRouterModel `json:"data"`
	}
	if err := json.Unmarshal(b, &out); err != nil || len(out.Data) == 0 {
		return SuggestedModels[0], nil
	}
	// Prefer Meta Muse Spark when the account can see it.
	for _, m := range out.Data {
		if m.ID == SuggestedModels[0] {
			return m.ID, nil
		}
	}
	return out.Data[0].ID, nil
}

// OpenRouterChain builds one client per USER-CHOSEN model, all sharing the
// same key — the pool then spreads load and fails over deterministically.
// models must come from user configuration (wizard-confirmed order,
// fallback_models); empty in = empty out, never silent defaults.
func OpenRouterChain(apiKey string, models []string) []Provider {
	out := make([]Provider, 0, len(models))
	for _, m := range models {
		if strings.TrimSpace(m) == "" {
			continue
		}
		out = append(out, &Client{APIKey: apiKey, BaseURL: OpenRouterBaseURL, Model: m})
	}
	return out
}
