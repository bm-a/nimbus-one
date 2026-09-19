// Package engine — blocking user-question tool.
//
// Mirrors OpenClaw (read-only reference in /data/.../tmp/openclaw-src):
//   - src/agents/tools/ask-user-tool.ts (questionId ask_sha256, reserved ->
//     prompting -> answerable -> resolving phases, timeout + cancel,
//     primary-only answering authority)
//
// Ask registers a pending question and blocks until Answer/Cancel or the
// timeout fires; the host delivers the user's reply via Answer.
package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"nimbus-one/internal/tools"
)

// PendingQ is one question awaiting a user answer.
type PendingQ struct {
	ID       string
	Question string
	Options  []string
	AskedAt  time.Time
}

type askOutcome struct {
	text      string
	cancelled bool
}

// AskTool blocks the agent loop on a user question until answered.
type AskTool struct {
	mu sync.Mutex
	// Pending holds questions awaiting answers, keyed by ask_<12hex> id.
	// Guarded by mu; mutate only via Ask/Answer/Cancel.
	Pending map[string]PendingQ
	// Timeout caps how long Ask waits. Non-positive means 120s.
	Timeout time.Duration
	// PrimaryOnly rejects Ask from non-primary sessions.
	PrimaryOnly bool
	// IsPrimary reports whether the current session may ask. Nil means
	// no gate is configured (asking allowed).
	IsPrimary func() bool

	waiters map[string]chan askOutcome
}

func (a *AskTool) timeout() time.Duration {
	if a.Timeout > 0 {
		return a.Timeout
	}
	return 120 * time.Second
}

// newAskID returns ask_<12 lowercase hex chars> (6 random bytes).
func newAskID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("ask_user: random id: %w", err)
	}
	return "ask_" + hex.EncodeToString(b[:]), nil
}

// Ask registers question and blocks until Answer(id, text), Cancel(id), the
// timeout, or ctx cancellation. Returns the answer text.
func (a *AskTool) Ask(question string, options []string) (string, error) {
	return a.ask(context.Background(), question, options)
}

func (a *AskTool) ask(ctx context.Context, question string, options []string) (string, error) {
	if strings.TrimSpace(question) == "" {
		return "", fmt.Errorf("ask_user: empty question")
	}
	if a.PrimaryOnly && a.IsPrimary != nil && !a.IsPrimary() {
		return "", fmt.Errorf("ask_user: only the primary session may ask questions")
	}
	id, err := newAskID()
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	if a.Pending == nil {
		a.Pending = map[string]PendingQ{}
	}
	if a.waiters == nil {
		a.waiters = map[string]chan askOutcome{}
	}
	a.Pending[id] = PendingQ{ID: id, Question: question, Options: append([]string(nil), options...), AskedAt: time.Now()}
	ch := make(chan askOutcome, 1)
	a.waiters[id] = ch
	timeout := a.timeout()
	a.mu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		a.remove(id)
		return "", ctx.Err()
	case <-timer.C:
		a.remove(id)
		return "", fmt.Errorf("ask_user: question %s timed out after %s", id, timeout)
	case out := <-ch:
		a.remove(id)
		if out.cancelled {
			return "", fmt.Errorf("ask_user: question %s was cancelled", id)
		}
		return out.text, nil
	}
}

func (a *AskTool) remove(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.Pending, id)
	delete(a.waiters, id)
}

// Answer delivers the user's reply to a pending question. It reports whether
// the id was pending — only the first answer wins, later ones return false.
func (a *AskTool) Answer(id, text string) bool {
	a.mu.Lock()
	ch, ok := a.waiters[id]
	if !ok {
		a.mu.Unlock()
		return false
	}
	delete(a.Pending, id)
	delete(a.waiters, id)
	a.mu.Unlock()
	ch <- askOutcome{text: text}
	return true
}

// Cancel aborts a pending question. It reports whether the id was pending.
func (a *AskTool) Cancel(id string) bool {
	a.mu.Lock()
	ch, ok := a.waiters[id]
	if !ok {
		a.mu.Unlock()
		return false
	}
	delete(a.Pending, id)
	delete(a.waiters, id)
	a.mu.Unlock()
	ch <- askOutcome{cancelled: true}
	return true
}

// Name implements tools.Tool.
func (a *AskTool) Name() string { return "ask_user" }

// Description implements tools.Tool.
func (a *AskTool) Description() string {
	return "Ask the user a question and block until they answer, the question is cancelled, or it times out."
}

// Parameters implements tools.Tool.
func (a *AskTool) Parameters() map[string]tools.Param {
	return map[string]tools.Param{
		"question": {Type: "string", Description: "Question text for the user.", Required: true},
		"options":  {Type: "array", Description: "Suggested answer options."},
	}
}

// Execute implements tools.Tool.
func (a *AskTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	q, _ := args["question"].(string)
	if strings.TrimSpace(q) == "" {
		return "", fmt.Errorf("ask_user: missing required argument %q", "question")
	}
	var opts []string
	switch t := args["options"].(type) {
	case []string:
		opts = t
	case []any:
		for i, e := range t {
			s, ok := e.(string)
			if !ok {
				return "", fmt.Errorf("ask_user: options[%d] is not a string", i)
			}
			opts = append(opts, s)
		}
	case string:
		if strings.TrimSpace(t) != "" {
			opts = []string{t}
		}
	}
	return a.ask(ctx, q, opts)
}
