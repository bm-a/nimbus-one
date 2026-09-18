package gateway

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
)

// RunREPL runs an interactive stdin/stdout loop against broker.
// user is the REPL identity (session key "repl/"+user).
// Commands: /exit, /reset, /help. EOF (Ctrl-D) exits cleanly.
func RunREPL(ctx context.Context, broker *Broker, user string) {
	if user == "" {
		user = "local"
	}
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	fmt.Fprintln(out, "nimbus-one repl — /help for commands, /exit or Ctrl-D to quit.")
	out.Flush()

	scanner := bufio.NewScanner(os.Stdin)
	// Allow long pastes.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		fmt.Fprint(out, "nimbus-one> ")
		out.Flush()

		if !scanner.Scan() {
			// EOF or error: exit quietly on EOF, report real errors.
			if err := scanner.Err(); err != nil {
				fmt.Fprintln(out, "\nread error: "+err.Error())
				out.Flush()
			} else {
				fmt.Fprintln(out, "")
				out.Flush()
			}
			return
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		switch line {
		case "/exit", "/quit", ":q":
			fmt.Fprintln(out, "bye.")
			out.Flush()
			return
		case "/reset":
			if broker != nil {
				broker.ResetSession("repl", user)
			}
			fmt.Fprintln(out, "(session reset)")
			out.Flush()
			continue
		case "/help", "/?":
			fmt.Fprintln(out, "commands: /help /mode /reset /exit")
			fmt.Fprintln(out, "  /mode [plan|build] — show or switch plan (read-only) / build (full) mode")
			out.Flush()
			continue
		}

		if strings.HasPrefix(line, "/mode") {
			arg := strings.TrimSpace(strings.TrimPrefix(line, "/mode"))
			if broker == nil {
				fmt.Fprintln(out, "(no broker)")
				out.Flush()
				continue
			}
			if arg == "" {
				fmt.Fprintln(out, "(mode: "+broker.Mode()+")")
				out.Flush()
				continue
			}
			switch strings.ToLower(arg) {
			case "plan", "build":
				broker.SetMode(strings.ToLower(arg))
				fmt.Fprintln(out, "(mode: "+broker.Mode()+")")
			default:
				fmt.Fprintln(out, "usage: /mode [plan|build]")
			}
			out.Flush()
			continue
		}

		if broker == nil {
			fmt.Fprintln(out, "(no broker)")
			out.Flush()
			continue
		}
		// Respect cancellation before doing work.
		select {
		case <-ctx.Done():
			return
		default:
		}
		reply := broker.Handle("repl", user, line)
		fmt.Fprintln(out, renderMarkdown(reply))
		out.Flush()
	}
}

// renderMarkdown passes text through with minimal ANSI highlighting:
// **bold** becomes bold via SGR 1. Unmatched markers are left as-is.
func renderMarkdown(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 16)
	bold := false
	for i := 0; i < len(s); {
		if i+1 < len(s) && s[i] == '*' && s[i+1] == '*' {
			if bold {
				b.WriteString("\x1b[0m")
			} else {
				b.WriteString("\x1b[1m")
			}
			bold = !bold
			i += 2
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	if bold {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}
