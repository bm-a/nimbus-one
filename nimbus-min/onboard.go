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

	// 2. API key.
	fmt.Fprintln(stdout, "\nNimbus needs an Anthropic API key (from https://console.anthropic.com/).")
	fmt.Fprintln(stdout, "It is stored in your config file (readable only by you), or you can")
	fmt.Fprintln(stdout, "leave this empty and set the ANTHROPIC_API_KEY environment variable instead.")
	key, err := ask("Anthropic API key (empty = use environment variable): ")
	if err != nil {
		return err
	}

	// 3. Workspace.
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

	// 4. Write config (atomic, 0600) + HTTP token for the app bridge.
	cfg := &Config{APIKey: key, Workspace: workspaceRoot, HTTPToken: newToken()}
	if err := saveConfig(cfg); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "\nDone! Your workspace is "+workspaceRoot)
	if key == "" {
		fmt.Fprintln(stdout, "Remember to set ANTHROPIC_API_KEY before your first request.")
	}
	fmt.Fprintln(stdout, "Try: nimbus-min run \"list my files\"")
	return nil
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
