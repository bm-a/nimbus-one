package main

import (
	"fmt"
	"os"
	"strings"
)

const version = "0.1.0"

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin *os.File, stdout, stderr *os.File) error {
	if len(args) == 0 {
		return usage(stdout)
	}
	switch args[0] {
	case "onboard":
		return onboard(stdin, stdout)
	case "run":
		if len(args) < 2 {
			return fmt.Errorf("usage: nimbus-min run \"<your request>\"")
		}
		return cmdRun(strings.Join(args[1:], " "), stdin, stdout, stderr)
	case "serve":
		addr := "127.0.0.1:8787"
		if len(args) > 1 {
			addr = args[1]
		}
		return cmdServe(addr, stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, "nimbus-min", version)
		return nil
	case "help", "--help", "-h":
		return usage(stdout)
	default:
		return fmt.Errorf("unknown command %q — try: onboard | run | serve | version | help", args[0])
	}
}

func usage(stdout *os.File) error {
	fmt.Fprintln(stdout, "Nimbus-One — a minimal coding assistant. One workspace, one model, five tools.")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "  nimbus-min onboard          first-time setup (key, workspace)")
	fmt.Fprintln(stdout, "  nimbus-min run \"<request>\"   do a coding task in your workspace")
	fmt.Fprintln(stdout, "  nimbus-min serve [addr]     local app bridge (default 127.0.0.1:8787)")
	fmt.Fprintln(stdout, "  nimbus-min version          print version")
	return nil
}

// cmdRun loads config, locks the jail, and runs one agent request.
func cmdRun(prompt string, stdin *os.File, stdout, stderr *os.File) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Workspace) == "" {
		return fmt.Errorf("no workspace set — run `nimbus-min onboard` first")
	}
	if err := cfg.validate(); err != nil {
		return err
	}
	key := cfg.apiKey()
	if key == "" {
		return fmt.Errorf("no Anthropic API key — run `nimbus-min onboard`, or set ANTHROPIC_API_KEY")
	}
	fmt.Fprintln(stderr, "Nimbus-One is working… (shell commands will pause for your \"yes\")")
	client := newAnthropicClient(key)
	answer, err := runAgent(client, stdin, stdout, isInteractive(), prompt, 25)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, answer)
	return nil
}
