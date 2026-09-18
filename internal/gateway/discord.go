package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Discord is a minimal REST-only Discord client bound to a Broker.
//
// Honest limitation: full gateway receive requires a websocket gateway
// connection (GUILD_MESSAGES / gateway intents), which is out of scope for
// a stdlib-only REST client. This type supports REST sends; RunGateway
// documents that and idles gracefully until ctx is done. Inbound Discord
// traffic should be fed via Broker.Handle("discord", user, text) by an
// external gateway process.
type Discord struct {
	Token  string
	Broker *Broker
}

// chunkDiscord splits s into <=limit byte chunks, preferring newlines.
// limit <= 0 defaults to 2000 (Discord message cap).
func chunkDiscord(s string, limit int) []string {
	if limit <= 0 {
		limit = 2000
	}
	if len(s) <= limit {
		return []string{s}
	}
	var out []string
	for len(s) > limit {
		cut := strings.LastIndex(s[:limit], "\n")
		if cut <= 0 {
			cut = limit
		}
		out = append(out, s[:cut])
		s = strings.TrimPrefix(s[cut:], "\n")
		if s == "" {
			break
		}
	}
	if s != "" {
		out = append(out, s)
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// Send posts text to channelID, chunked to 2000 chars per message.
func (d *Discord) Send(ctx context.Context, channelID, text string) error {
	if d.Token == "" {
		return fmt.Errorf("discord: empty token")
	}
	if channelID == "" {
		return fmt.Errorf("discord: empty channel id")
	}
	for _, chunk := range chunkDiscord(text, 2000) {
		if err := d.sendOne(ctx, channelID, chunk); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	return nil
}

// sendOne posts a single message object.
func (d *Discord) sendOne(ctx context.Context, channelID, content string) error {
	body, err := json.Marshal(map[string]string{"content": content})
	if err != nil {
		return err
	}
	endpoint := "https://discord.com/api/v10/channels/" + channelID + "/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bot "+d.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discord send: status %s", resp.Status)
	}
	return nil
}

// RunGateway is intentionally REST-only: it does not fake websocket
// presence or synthesize activity. It blocks until ctx is cancelled so
// supervisors can run it alongside other gateways uniformly.
func (d *Discord) RunGateway(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}
