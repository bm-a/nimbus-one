package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// cmdModels lists every provider (OpenClaw parity): cloud with their
// key env vars and defaults, local with no key, and the custom-override
// escape hatch. The configured provider is marked.
func cmdModels(stdout io.Writer) error {
	cfg, _ := loadConfig()
	current := currentProviderID(cfg)
	var cloud, local strings.Builder
	for _, p := range providers {
		key := "no key needed"
		if len(p.KeyEnvs) > 0 {
			key = strings.Join(p.KeyEnvs, " / ")
		}
		model := p.DefaultModel
		if model == "" {
			model = "(model required)"
		}
		mark := " "
		if current != "" && strings.EqualFold(current, p.ID) {
			mark = "*"
		}
		line := fmt.Sprintf("%s %-18s %-28s model: %-32s key: %s", mark, p.ID, p.Name, model, key)
		if p.Local {
			local.WriteString(line + "\n")
		} else {
			cloud.WriteString(line + "\n")
		}
	}
	fmt.Fprintln(stdout, "Providers (* = configured). Choose in `onboard`, per-run with --provider, or via NIMBUS_PROVIDER.")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Cloud (API key):")
	fmt.Fprint(stdout, cloud.String())
	fmt.Fprintln(stdout, "Local (no key, self-hosted):")
	fmt.Fprint(stdout, local.String())
	fmt.Fprintln(stdout, "Custom (any OpenAI-compatible server, e.g. vLLM / SGLang / LiteLLM):")
	fmt.Fprintln(stdout, "  --base-url URL (or NIMBUS_BASE_URL) + key via NIMBUS_API_KEY,")
	fmt.Fprintln(stdout, "  paired with the matching provider shape (usually an openai-family id).")
	fmt.Fprintln(stdout, "Not carried over: cloud SDK-auth (bedrock, vertex, azure), OAuth CLIs")
	fmt.Fprintln(stdout, "(claude-cli, codex, copilot), ambiguous endpoints (qwen main,")
	fmt.Fprintln(stdout, "alibaba). Full reasons: DECISIONS.md in the repo.")
	return nil
}

// currentProviderID reports the configured provider id (for the `*`
// marker). Unconfigured just means no marker.
func currentProviderID(cfg *Config) string {
	if cfg == nil {
		return "anthropic"
	}
	return firstNonEmpty(os.Getenv("NIMBUS_PROVIDER"), cfg.Provider, "anthropic")
}

// helpTopic prints per-command detail.
func helpTopic(stdout *os.File, topic string) error {
	switch topic {
	case "run":
		fmt.Fprintln(stdout, "usage: nimbus-min run [--provider ID] [--model M] [--base-url URL] \"<request>\"")
		fmt.Fprintln(stdout, "Runs one agent turn loop (max 25 turns) in your workspace.")
		fmt.Fprintln(stdout, "Provider/model/base fall back to flags → env (NIMBUS_PROVIDER/_MODEL/_BASE_URL) → config → provider default.")
	case "onboard":
		fmt.Fprintln(stdout, "usage: nimbus-min onboard")
		fmt.Fprintln(stdout, "First-time setup: provider → model → API key → workspace folder. Re-runnable; overwrites config.")
	case "models":
		fmt.Fprintln(stdout, "usage: nimbus-min models")
		fmt.Fprintln(stdout, "Lists all providers, key env vars, and default models.")
	case "serve":
		fmt.Fprintln(stdout, "usage: nimbus-min serve [addr]   (default 127.0.0.1:8787)")
		fmt.Fprintln(stdout, "Control page + app bridge (connect, chat.send, sessions.list). Headless: shell is refused, never run.")
	default:
		return usage(stdout)
	}
	return nil
}
