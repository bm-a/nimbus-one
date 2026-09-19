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
		return cmdRunFlags(args[1:], stdin, stdout, stderr)
	case "serve":
		addr := "127.0.0.1:8787"
		if len(args) > 1 {
			addr = args[1]
		}
		return cmdServe(addr, stdout, stderr)
	case "models":
		return cmdModels(stdout)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, "nimbus-min", version)
		return nil
	case "help", "--help", "-h":
		if len(args) > 1 {
			return helpTopic(stdout, args[1])
		}
		return usage(stdout)
	default:
		return fmt.Errorf("unknown command %q — try: onboard | run | models | serve | version | help", args[0])
	}
}

func usage(stdout *os.File) error {
	fmt.Fprintln(stdout, "Nimbus-One — a minimal coding assistant. One workspace, five tools, your choice of provider.")
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "  nimbus-min onboard                       first-time setup (provider, key, workspace)")
	fmt.Fprintln(stdout, "  nimbus-min run \"<request>\"                do a coding task in your workspace")
	fmt.Fprintln(stdout, "  nimbus-min run --provider ID [--model M] \"<request>\"   one-off provider/model override")
	fmt.Fprintln(stdout, "  nimbus-min models                        list all providers (OpenClaw parity)")
	fmt.Fprintln(stdout, "  nimbus-min serve [addr]                  local app bridge (default 127.0.0.1:8787)")
	fmt.Fprintln(stdout, "  nimbus-min version                       print version")
	return nil
}

// cmdRunFlags parses --provider/--model/--base-url overrides, then runs.
func cmdRunFlags(args []string, stdin *os.File, stdout, stderr *os.File) error {
	var flagProvider, flagModel, flagBase string
	var prompt []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		val := ""
		if j := strings.Index(a, "="); strings.HasPrefix(a, "--") && j > 0 {
			val = a[j+1:]
			a = a[:j]
		} else if strings.HasPrefix(a, "--") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
			val = args[i+1]
			i++
		}
		switch a {
		case "--provider":
			flagProvider = val
		case "--model":
			flagModel = val
		case "--base-url":
			flagBase = val
		default:
			prompt = append(prompt, args[i])
		}
	}
	if len(prompt) == 0 {
		return fmt.Errorf("usage: nimbus-min run [--provider ID] [--model M] [--base-url URL] \"<your request>\"")
	}
	return cmdRun(strings.Join(prompt, " "), flagProvider, flagModel, flagBase, stdin, stdout, stderr)
}

// cmdRun loads config, locks the jail, resolves the provider, and runs
// one agent request.
func cmdRun(prompt, flagProvider, flagModel, flagBase string, stdin *os.File, stdout, stderr *os.File) error {
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
	r, err := resolveClient(cfg, flagProvider, flagModel, flagBase)
	if err != nil {
		return err
	}
	fmt.Fprintf(stderr, "Nimbus-One is working (%s / %s)… (shell commands will pause for your \"yes\")\n", r.provider.ID, r.model)
	answer, err := runAgent(r.client, stdin, stdout, isInteractive(), prompt, 25)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, answer)
	return nil
}
