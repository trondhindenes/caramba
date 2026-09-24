package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxSlackText stays under Slack's 40k character limit for message text.
const maxSlackText = 39000

// webhook posts JSON payloads to a Slack incoming webhook.
type webhook struct {
	url    string
	client *http.Client
}

func newWebhook(webhookURL string) webhook {
	return webhook{url: webhookURL, client: &http.Client{Timeout: 15 * time.Second}}
}

// Slack sends a plain text message: the title in bold above the body, both
// Slack mrkdwn.
type Slack struct{ webhook }

func NewSlack(webhookURL string) *Slack {
	return &Slack{newWebhook(webhookURL)}
}

func (s *Slack) Send(ctx context.Context, msg *Message) error {
	text := strings.TrimSpace(msg.Body)
	if title := strings.TrimSpace(msg.Title); title != "" {
		text = "*" + title + "*\n" + text
	}
	return s.post(ctx, map[string]string{"text": truncate(text)})
}

// SlackAttachment sends a legacy Slack attachment, the only message form
// with a colored side bar. The title links to the alert in Grafana.
type SlackAttachment struct {
	webhook
	colors *colorizer
}

func NewSlackAttachment(webhookURL string, colors *colorizer) *SlackAttachment {
	return &SlackAttachment{webhook: newWebhook(webhookURL), colors: colors}
}

func (s *SlackAttachment) Send(ctx context.Context, msg *Message) error {
	title := strings.TrimSpace(msg.Title)
	attachment := map[string]any{
		"fallback":  title,
		"color":     s.colors.color(msg),
		"title":     title,
		"text":      truncate(strings.TrimSpace(msg.Body)),
		"mrkdwn_in": []string{"text"},
		"footer":    "caramba",
		"ts":        time.Now().Unix(),
	}
	if msg.Link != "" {
		attachment["title_link"] = msg.Link
	}
	return s.post(ctx, map[string]any{"attachments": []any{attachment}})
}

func truncate(text string) string {
	if len(text) > maxSlackText {
		return text[:maxSlackText] + "\n…(truncated)"
	}
	return text
}

func (w webhook) post(ctx context.Context, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return permanentError{err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return permanentError{err}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.client.Do(req)
	if err != nil {
		// Strip the URL: the webhook URL is the secret.
		return fmt.Errorf("posting to slack: %w", unwrapURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	err = fmt.Errorf("slack returned %d: %s", resp.StatusCode, strings.TrimSpace(string(detail)))
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return err
	}
	return permanentError{err}
}

// unwrapURLError drops the request URL that *url.Error embeds in its
// message, so webhook URLs (secrets) never reach logs or the GUI.
func unwrapURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
