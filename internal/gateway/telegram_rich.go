// Telegram rich updates: callback queries (inline buttons), poll answers,
// edited messages, the /command registry, and session trimming.
//
// Reference (read-only mirror): /data/data/com.termux/files/home/tmp/openclaw-src
//   - extensions/telegram/src (callbacks/questions/menus, approvals,
//     poll answers, typing indicators, progress edits)
//   - channels/thread-bindings (thread session suffix — Telegram has no
//     threads; replies stay per chat here)
package gateway

import (
	"context"
	"sort"
	"strconv"
	"strings"
)

// tgCallbackQuery is an incoming inline-button press.
type tgCallbackQuery struct {
	ID      string     `json:"id"`
	From    *tgUser    `json:"from,omitempty"`
	Message *tgMessage `json:"message,omitempty"`
	Data    string     `json:"data,omitempty"`
}

// tgPollAnswer is an incoming poll vote.
type tgPollAnswer struct {
	PollID    string  `json:"poll_id"`
	User      *tgUser `json:"user,omitempty"`
	OptionIDs []int   `json:"option_ids,omitempty"`
}

// answerCallbackQuery acknowledges a button press so the client's loading
// spinner stops. Best effort: errors are dropped.
func (t *Telegram) answerCallbackQuery(ctx context.Context, queryID string) {
	if t == nil || strings.TrimSpace(queryID) == "" {
		return
	}
	_, _ = t.postTelegramMethod(ctx, "answerCallbackQuery", map[string]any{
		"callback_query_id": queryID,
	})
}

// callbackSender extracts the pressing user's id, falling back to the
// message chat id when From is absent.
func callbackSender(q tgCallbackQuery) string {
	if q.From != nil {
		return strings.TrimSpace(strconv.FormatInt(q.From.ID, 10))
	}
	if q.Message != nil {
		return strings.TrimSpace(strconv.FormatInt(q.Message.Chat.ID, 10))
	}
	return ""
}

// handleCallback answers the query, then routes the button payload through
// the broker as "button:<data>" and replies chunked to the source chat.
func (t *Telegram) handleCallback(ctx context.Context, q tgCallbackQuery) {
	t.answerCallbackQuery(ctx, q.ID)
	if t.Broker == nil {
		return
	}
	sender := callbackSender(q)
	if !t.Allowed(sender) {
		return
	}
	chatID := int64(0)
	if q.Message != nil {
		chatID = q.Message.Chat.ID
	}
	if strings.TrimSpace(q.Data) == "" {
		return
	}
	t.sendTyping(ctx, chatID)
	reply := t.Broker.Handle("telegram", sender, "button:"+q.Data)
	t.replyChunked(ctx, chatID, reply)
}

// handlePollAnswer routes a poll vote through the broker as
// "poll:<pollID>:<comma-joined-option-ids>" and replies chunked. Poll
// answers carry no chat, so the reply goes to the voter's private chat
// (user id == private chat id in the Bot API).
func (t *Telegram) handlePollAnswer(ctx context.Context, a tgPollAnswer) {
	if t.Broker == nil {
		return
	}
	var sender string
	var chatID int64
	if a.User != nil {
		sender = strings.TrimSpace(strconv.FormatInt(a.User.ID, 10))
		chatID = a.User.ID
	}
	if sender == "" || !t.Allowed(sender) {
		return
	}
	opts := make([]string, 0, len(a.OptionIDs))
	for _, o := range a.OptionIDs {
		opts = append(opts, strconv.Itoa(o))
	}
	t.sendTyping(ctx, chatID)
	reply := t.Broker.Handle("telegram", sender, "poll:"+a.PollID+":"+strings.Join(opts, ","))
	t.replyChunked(ctx, chatID, reply)
}

// handleEdited re-runs an edited message's text through the broker and
// updates the reply in place via Edit. When the edit fails (e.g. the
// message is too old or was never answered), it falls back to a fresh
// chunked reply so the user still sees the answer.
func (t *Telegram) handleEdited(ctx context.Context, m tgMessage) {
	if t.Broker == nil {
		return
	}
	var sender string
	if m.From != nil {
		sender = strings.TrimSpace(strconv.FormatInt(m.From.ID, 10))
	} else {
		sender = strings.TrimSpace(strconv.FormatInt(m.Chat.ID, 10))
	}
	if !t.Allowed(sender) {
		return
	}
	if t.Policy != nil && !t.Policy.IsGroupAllowed(m.Chat.ID, m.Chat.Type) {
		return
	}
	text := strings.TrimSpace(m.Text)
	if text == "" {
		return
	}
	t.sendTyping(ctx, m.Chat.ID)
	reply := t.Broker.Handle("telegram", sender, text)
	if strings.TrimSpace(reply) == "" {
		return
	}
	if err := t.Edit(ctx, m.Chat.ID, m.MessageID, reply); err != nil {
		t.replyChunked(ctx, m.Chat.ID, reply)
	}
}

// replyChunked sends text to chatID in Policy.ChunkLimit() chunks,
// recording per-chunk health like handleUpdate does.
func (t *Telegram) replyChunked(ctx context.Context, chatID int64, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	limit := 4000
	if t.Policy != nil {
		limit = t.Policy.ChunkLimit()
	}
	for _, chunk := range chunkMessage(text, limit) {
		select {
		case <-ctx.Done():
			return
		default:
		}
		err := t.sendMessage(ctx, chatID, chunk)
		if t.Health != nil {
			if err != nil {
				t.Health.RecordFailure(err)
			} else {
				t.Health.RecordSuccess()
			}
		}
	}
}

// TelegramCommand handles a /command. chatID is the reply target, sender
// is the invoking user id (for session-scoped commands like /reset), and
// args is the text after the command name.
type TelegramCommand func(ctx context.Context, chatID int64, sender, args string)

// RegisterCommand adds (or replaces) a /command handler. Safe for
// concurrent use; typically called during setup before polling starts.
func (t *Telegram) RegisterCommand(name string, fn TelegramCommand) {
	name = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), "/")))
	if name == "" || fn == nil {
		return
	}
	t.cmdMu.Lock()
	defer t.cmdMu.Unlock()
	if t.Commands == nil {
		t.Commands = make(map[string]TelegramCommand)
	}
	t.Commands[name] = fn
}

// ensureDefaultCommands installs the built-in /help and /reset handlers
// once. /speak stays inline in handleUpdate (telegram.go) because it needs
// the audio path, not the broker.
func (t *Telegram) ensureDefaultCommands() {
	t.cmdMu.Lock()
	defer t.cmdMu.Unlock()
	if t.Commands == nil {
		t.Commands = make(map[string]TelegramCommand)
	}
	if _, ok := t.Commands["help"]; !ok {
		t.Commands["help"] = func(ctx context.Context, chatID int64, sender, args string) {
			t.cmdMu.Lock()
			names := make([]string, 0, len(t.Commands))
			for n := range t.Commands {
				names = append(names, "/"+n)
			}
			t.cmdMu.Unlock()
			sort.Strings(names)
			_ = t.sendMessage(ctx, chatID, "commands: "+strings.Join(names, ", "))
		}
	}
	if _, ok := t.Commands["reset"]; !ok {
		t.Commands["reset"] = func(ctx context.Context, chatID int64, sender, args string) {
			if t.Broker != nil {
				t.Broker.ResetSession("telegram", sender)
			}
			_ = t.sendMessage(ctx, chatID, "session reset — fresh context from here.")
		}
	}
}

// parseCommand splits "/name@bot args" into name and args. ok is false when
// text is not a /command.
func parseCommand(text string) (name, args string, ok bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", "", false
	}
	// Command token ends at the first whitespace; strip @bot suffixes
	// so group invocations like /help@nimbusbot dispatch correctly.
	token := text
	rest := ""
	if idx := strings.IndexAny(text, " \t\n"); idx >= 0 {
		token = text[:idx]
		rest = strings.TrimSpace(text[idx+1:])
	}
	name = strings.TrimPrefix(token, "/")
	if idx := strings.Index(name, "@"); idx >= 0 {
		name = name[:idx]
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "", "", false
	}
	return name, rest, true
}

// runCommand dispatches /commands through the registry. Returns true when a
// registered command handled the text. Unknown /commands return false so
// they fall through to the broker (the engine may answer them).
func (t *Telegram) runCommand(ctx context.Context, chatID int64, sender, text string) bool {
	name, args, ok := parseCommand(text)
	if !ok {
		return false
	}
	t.ensureDefaultCommands()
	t.cmdMu.Lock()
	fn := t.Commands[name]
	t.cmdMu.Unlock()
	if fn == nil {
		return false
	}
	fn(ctx, chatID, sender, args)
	return true
}

// TrimSession keeps only the last n messages of channel/user's history.
// It reads the full history, resets, and re-appends the tail. n <= 0 or
// n >= len(history) is a no-op (never an accidental wipe). Nil-safe.
func (b *Broker) TrimSession(channel, user string, n int) {
	if b == nil || n <= 0 {
		return
	}
	key := Key(channel, user)
	store := b.SessionsStore()
	full := store.History(key, 0)
	if n >= len(full) {
		return
	}
	tail := append([]SessionMsg(nil), full[len(full)-n:]...)
	store.Reset(key)
	for _, m := range tail {
		store.Append(key, m.Role, m.Content)
	}
}
