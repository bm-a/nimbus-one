package main

import (
	"fmt"
	"os"
	"strings"
)

// resolved is a ready-to-call model backend.
type resolved struct {
	client   modelClient
	provider Provider
	model    string
	base     string
}

// resolveClient turns config + env into a backend. Precedence:
// provider: --flag > NIMBUS_PROVIDER > config > "anthropic"
// model: --flag > NIMBUS_MODEL > config > provider default (required
// when the provider has none). base: --flag > NIMBUS_BASE_URL >
// config > provider default. key: config api_key > provider envs >
// NIMBUS_API_KEY (skipped for local providers).
func resolveClient(cfg *Config, flagProvider, flagModel, flagBase string) (*resolved, error) {
	id := firstNonEmpty(flagProvider, os.Getenv("NIMBUS_PROVIDER"), cfg.Provider, "anthropic")
	p, err := lookupProvider(id)
	if err != nil {
		return nil, err
	}
	base := firstNonEmpty(flagBase, os.Getenv("NIMBUS_BASE_URL"), cfg.BaseURL, p.Base)
	model := firstNonEmpty(flagModel, os.Getenv("NIMBUS_MODEL"), cfg.Model, p.DefaultModel)
	if model == "" {
		return nil, fmt.Errorf("provider %q has no default model — set one via `nimbus-min onboard`, `model` in config, or --model (see `nimbus-min models`)", p.ID)
	}
	key := resolveKey(cfg, p)
	if key == "" && !p.Local {
		return nil, fmt.Errorf("no API key for %s — run `nimbus-min onboard`, set %s, or set NIMBUS_API_KEY", p.Name, strings.Join(p.KeyEnvs, " or "))
	}
	guard := guardedHTTPClientFor(base)
	var client modelClient
	if p.Family == familyAnthropic {
		client = &anthropicClient{http: guard, key: key, base: base, model: model}
	} else {
		client = newOpenAIClient(guard, key, base, model)
	}
	return &resolved{client: client, provider: p, model: model, base: base}, nil
}

// resolveKey finds the key without ever logging it.
func resolveKey(cfg *Config, p Provider) string {
	if strings.TrimSpace(cfg.APIKey) != "" {
		return strings.TrimSpace(cfg.APIKey)
	}
	for _, env := range p.KeyEnvs {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return v
		}
	}
	return strings.TrimSpace(os.Getenv("NIMBUS_API_KEY"))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
