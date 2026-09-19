package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// confirmShell is the single chokepoint for shell execution. There is
// deliberately NO pre-approval list, NO allowlist, and NO bypass flag:
// every shell command shows its exact text, a plain-English explanation,
// and requires the user to type "yes".
//
//   - Interactive terminal: prompt and read one line. Anything other than
//     exactly "yes" (case-insensitive, trimmed) refuses.
//   - Headless (no terminal / piped stdin / serve mode): refuse outright.
//     The refusal message tells the user to re-run in a terminal.
//
// stdin/stdout are parameters (not globals) so the gate is fully testable.
func confirmShell(command, explanation string, stdin io.Reader, stdout io.Writer, interactive bool) error {
	fmt.Fprintln(stdout, "Shell command requested:")
	fmt.Fprintf(stdout, "  $ %s\n", command)
	fmt.Fprintf(stdout, "  What this does: %s\n", explanation)
	if !interactive {
		return fmt.Errorf("refused: shell commands need a terminal — re-run in a terminal and type \"yes\" to approve")
	}
	fmt.Fprint(stdout, "Type \"yes\" to run it, anything else to skip: ")
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && len(line) == 0 {
		return fmt.Errorf("refused: no answer (shell command skipped)")
	}
	if strings.ToLower(strings.TrimSpace(line)) != "yes" {
		return fmt.Errorf("refused: shell command skipped (you did not type \"yes\")")
	}
	return nil
}

// explainShell produces the plain-English line shown before confirmation.
// It names the program and its target; unknown shapes get an honest
// generic explanation rather than a fabricated one.
func explainShell(command string) string {
	toks := splitShellTokens(command)
	if len(toks) == 0 {
		return "run an (empty?) shell command"
	}
	prog := toks[0]
	rest := strings.Join(toks[1:], " ")
	if len(rest) > 120 {
		rest = rest[:120] + "…"
	}
	switch prog {
	case "ls":
		return "list files in the workspace"
	case "cat":
		return fmt.Sprintf("show the contents of %s", orSomething(rest))
	case "echo":
		return "print text to the screen"
	case "go":
		return fmt.Sprintf("run the Go tool (%s)", orSomething(rest))
	case "grep":
		return fmt.Sprintf("search file contents for %s", orSomething(rest))
	case "mkdir":
		return fmt.Sprintf("create folder(s) %s", orSomething(rest))
	case "rm":
		return fmt.Sprintf("DELETE %s (cannot be undone)", orSomething(rest))
	case "mv":
		return fmt.Sprintf("move/rename %s", orSomething(rest))
	case "cp":
		return fmt.Sprintf("copy %s", orSomething(rest))
	}
	if rest == "" {
		return fmt.Sprintf("run the %q program", prog)
	}
	return fmt.Sprintf("run %q with %s", prog, rest)
}

func orSomething(s string) string {
	if strings.TrimSpace(s) == "" {
		return "something in the workspace"
	}
	return s
}

// isInteractive reports whether stdin is a terminal. Overridable in tests
// via the NIMBUS_MIN_ASSUME_TTY env hook (tests only).
func isInteractive() bool {
	if os.Getenv("NIMBUS_MIN_ASSUME_TTY") != "" {
		return true
	}
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
