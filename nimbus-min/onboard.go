package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// onboard runs the first-run wizard. Order mirrors OpenClaw's flow
// (validate → consent → key → workspace → write config), cut to the
// minimal scope: no providers to choose, no gateway, no memory import.
func onboard(stdin io.Reader, stdout io.Writer) error {
	in := bufio.NewReader(stdin)
	ask := func(prompt string) (string, error) {
		fmt.Fprint(stdout, prompt)
		line, err := in.ReadString('\n')
		if err != nil && len(line) == 0 {
			return "", fmt.Errorf("no answer — onboard cancelled, nothing changed")
		}
		return strings.TrimSpace(line), nil
	}

	fmt.Fprintln(stdout, "Welcome to Nimbus-One — your own coding assistant.")
	fmt.Fprintln(stdout, "This takes a minute. Nothing is changed until the end.")
	fmt.Fprintln(stdout)

	// 1. Consent / safety notice (plain English, up front).
	fmt.Fprintln(stdout, "How it works, in plain English:")
	fmt.Fprintln(stdout, "- Nimbus reads and changes files ONLY inside one folder you choose (the workspace).")
	fmt.Fprintln(stdout, "- Before EVERY shell command it shows you the exact command and waits for you to type \"yes\".")
	fmt.Fprintln(stdout, "- It only ever talks to Anthropic's servers (api.anthropic.com) — nothing else on the network.")
	fmt.Fprintln(stdout)
	ok, err := ask("Understand and continue? (yes/no) ")
	if err != nil {
		return err
	}
	if strings.ToLower(ok) != "yes" {
		return fmt.Errorf("onboard cancelled — nothing changed")
	}

	// 2. Provider (numbered populars + any id from `models`).
	fmt.Fprintln(stdout, "\nChoose a provider (same list as `nimbus-min models`):")
	for i, id := range popularProviders {
		p, _ := lookupProvider(id)
		fmt.Fprintf(stdout, "  %d. %-14s %s\n", i+1, p.ID, p.Name)
	}
	fmt.Fprintln(stdout, "  (or type any other provider id)")
	provAnswer, err := ask("Provider [1 = anthropic]: ")
	if err != nil {
		return err
	}
	prov, err := parseProviderAnswer(provAnswer)
	if err != nil {
		return err
	}

	// 3. Model (default shown; required when the provider has none).
	modelPrompt := fmt.Sprintf("Model [%s]: ", prov.DefaultModel)
	if prov.DefaultModel == "" {
		modelPrompt = "Model (required — this provider has no default; see `nimbus-min models`): "
	}
	model, err := ask(modelPrompt)
	if err != nil {
		return err
	}
	if model == "" {
		model = prov.DefaultModel
	}
	if model == "" {
		return fmt.Errorf("no model chosen — re-run onboard with a model id from `nimbus-min models`")
	}

	// 4. API key (skipped for local providers).
	key := ""
	if !prov.Local {
		fmt.Fprintf(stdout, "\nNimbus needs an API key for %s.\n", prov.Name)
		fmt.Fprintf(stdout, "Stored in your config file (readable only by you), or leave empty\n")
		fmt.Fprintf(stdout, "and set %s instead.\n", strings.Join(append(prov.KeyEnvs, "NIMBUS_API_KEY"), " or "))
		key, err = ask("API key (empty = use environment variable): ")
		if err != nil {
			return err
		}
	} else {
		fmt.Fprintf(stdout, "\n%s needs no key (local server).\n", prov.Name)
	}

	// 5. Workspace.
	fmt.Fprintln(stdout, "\nChoose the workspace folder. Nimbus can ONLY touch files inside it.")
	def := defaultWorkspace()
	ws, err := ask(fmt.Sprintf("Workspace folder [%s]: ", def))
	if err != nil {
		return err
	}
	if ws == "" {
		ws = def
	}
	if err := os.MkdirAll(ws, 0o755); err != nil {
		return fmt.Errorf("cannot create workspace %q: %v", ws, err)
	}
	if err := initJail(ws); err != nil {
		return fmt.Errorf("workspace problem: %v", err)
	}

	// 6. Write config (atomic, 0600) + HTTP token for the app bridge.
	cfg := &Config{Provider: prov.ID, Model: model, APIKey: key, Workspace: workspaceRoot, HTTPToken: newToken()}
	if err := saveConfig(cfg); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "\nDone! Provider %s, model %s, workspace %s\n", prov.ID, model, workspaceRoot)
	if key == "" && !prov.Local {
		fmt.Fprintf(stdout, "Remember to set %s before your first request.\n", strings.Join(append(prov.KeyEnvs, "NIMBUS_API_KEY"), " or "))
	}
	fmt.Fprintln(stdout, "Try: nimbus-min run \"list my files\"")
	return nil
}

// popularProviders are the onboard shortlist (numbers); any table id is
// accepted too. Ordered by general coding strength, anthropic first
// (the default).
var popularProviders = []string{
	"anthropic", "openai", "google", "deepseek", "openrouter",
	"groq", "mistral", "ollama", "kimi", "xai",
}

// parseProviderAnswer accepts "" (default), a shortlist number, or any
// provider id from the table.
func parseProviderAnswer(answer string) (Provider, error) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return lookupProvider("anthropic")
	}
	for i, id := range popularProviders {
		if answer == fmt.Sprint(i+1) {
			return lookupProvider(id)
		}
	}
	return lookupProvider(answer)
}

// defaultWorkspace suggests ~/nimbus-workspace.
func defaultWorkspace() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "nimbus-workspace"
	}
	return home + string(os.PathSeparator) + "nimbus-workspace"
}

// newToken mints the local HTTP token for the app bridge.
func newToken() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}
