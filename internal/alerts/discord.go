package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"git.cer.sh/axodouble/quptime/internal/config"
)

// discordTimeout caps how long a single webhook POST is allowed to
// take.
const discordTimeout = 10 * time.Second

// discordPayload is the minimum shape the Discord webhook API
// accepts. We do not use embeds — plain text keeps the payload
// trivial to read in operator-side logs.
type discordPayload struct {
	Content string `json:"content"`
}

// sendDiscord posts msg.Subject + body to the configured webhook URL.
// When the alert has a custom BodyTemplate, the rendered body is shipped
// verbatim — the operator has opted out of the default subject header
// and code-block wrapping in favour of their own formatting.
func sendDiscord(a *config.Alert, msg Message) error {
	if a.DiscordWebhook == "" {
		return errors.New("discord webhook url not set")
	}

	var content string
	if a.BodyTemplate != "" {
		content = msg.Body
	} else {
		content = msg.Subject + "\n```\n" + msg.Body + "\n```"
	}
	raw, err := json.Marshal(discordPayload{Content: content})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), discordTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.DiscordWebhook, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("discord webhook: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("discord webhook status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
