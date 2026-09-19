// Discord rich messaging: thread creation, message edits (approval
// updates), and guild-scoped user routing.
//
// Reference (read-only mirror): /data/data/com.termux/files/home/tmp/openclaw-src
//   - extensions/discord/src (threads, approval-message-update flows,
//     guild/role routing)
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

// RouteUser maps (guildID, userID) to the broker user key. When GuildRoutes
// holds a prefix for the guild, the engine sees "<prefix><userID>" so
// sessions stay per-server; otherwise the bare userID is returned.
// Empty guildID (DMs) never gets a prefix. Nil-safe.
func (d *Discord) RouteUser(guildID, userID string) string {
	if d == nil {
		return userID
	}
	guildID = strings.TrimSpace(guildID)
	if guildID == "" {
		return userID
	}
	if prefix := strings.TrimSpace(d.GuildRoutes[guildID]); prefix != "" {
		return prefix + userID
	}
	return userID
}

// postJSON POSTs/PATCHes a JSON body with Bot auth and bounds the response.
// 2xx returns the decoded-agnostic raw body; other statuses are errors.
func (d *Discord) restJSON(ctx context.Context, method, url string, payload any) ([]byte, error) {
	if d.Token == "" {
		return nil, fmt.Errorf("discord: empty token")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("discord: encode payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bot "+d.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("discord: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("discord: status %s: %s", resp.Status, truncateBody(string(data), 500))
	}
	return data, nil
}

// EditMessage replaces a message's content via PATCH. This is the approval
// flow primitive: post a pending-approval message, then edit it in place
// with the decision instead of spamming the channel.
func (d *Discord) EditMessage(ctx context.Context, channelID, msgID, text string) error {
	if channelID == "" {
		return fmt.Errorf("discord: empty channel id")
	}
	if msgID == "" {
		return fmt.Errorf("discord: empty message id")
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("discord: empty text")
	}
	endpoint := d.restBase() + "/channels/" + channelID + "/messages/" + msgID
	_, err := d.restJSON(ctx, http.MethodPatch, endpoint, map[string]string{"content": text})
	return err
}

// SendThread creates a public thread on channelID and posts content into
// it, chunked to 2000 chars per message. Returns the thread's channel id.
func (d *Discord) SendThread(ctx context.Context, channelID, name, content string) (string, error) {
	if channelID == "" {
		return "", fmt.Errorf("discord: empty channel id")
	}
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("discord: empty thread name")
	}
	endpoint := d.restBase() + "/channels/" + channelID + "/threads"
	data, err := d.restJSON(ctx, http.MethodPost, endpoint, map[string]any{
		"name":                  name,
		"type":                  11, // GUILD_PUBLIC_THREAD (no parent message)
		"auto_archive_duration": 60,
	})
	if err != nil {
		return "", err
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &created); err != nil {
		return "", fmt.Errorf("discord: decode thread response: %w", err)
	}
	if strings.TrimSpace(created.ID) == "" {
		return "", fmt.Errorf("discord: missing thread id in response: %s", truncateBody(string(data), 500))
	}
	for _, chunk := range chunkDiscord(content, 2000) {
		msgURL := d.restBase() + "/channels/" + created.ID + "/messages"
		if _, err := d.restJSON(ctx, http.MethodPost, msgURL, map[string]string{"content": chunk}); err != nil {
			return created.ID, err
		}
		select {
		case <-ctx.Done():
			return created.ID, ctx.Err()
		default:
		}
	}
	return created.ID, nil
}
