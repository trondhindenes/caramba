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
	"unicode/utf8"
)

// maxSlackText stays under Slack's 40k character limit for message text.
const maxSlackText = 39000

// webhook posts JSON payloads to an incoming webhook.
type webhook struct {
	service string // named in errors
	url     string
	client  *http.Client
}

func newWebhook(service, webhookURL string) webhook {
	return webhook{service: service, url: webhookURL, client: &http.Client{Timeout: 15 * time.Second}}
}

// Slack sends a plain text message: the title in bold above the body, both
// Slack mrkdwn.
type Slack struct{ webhook }

func NewSlack(webhookURL string) *Slack {
	return &Slack{newWebhook("slack", webhookURL)}
}

func (s *Slack) Send(ctx context.Context, msg *Message) error {
	text := strings.TrimSpace(msg.Body)
	if title := strings.TrimSpace(msg.Title); title != "" {
		text = "*" + title + "*\n" + text
	}
	return s.post(ctx, map[string]string{"text": truncate(text, maxSlackText)})
}

// SlackAttachment sends a legacy Slack attachment, the only message form
// with a colored side bar. The title links to the alert in Grafana.
type SlackAttachment struct {
	webhook
	colors *colorizer
}

func NewSlackAttachment(webhookURL string, colors *colorizer) *SlackAttachment {
	return &SlackAttachment{webhook: newWebhook("slack", webhookURL), colors: colors}
}

func (s *SlackAttachment) Send(ctx context.Context, msg *Message) error {
	title := strings.TrimSpace(msg.Title)
	attachment := map[string]any{
		"fallback":  title,
		"color":     s.colors.color(msg),
		"title":     title,
		"text":      truncate(strings.TrimSpace(msg.Body), maxSlackText),
		"mrkdwn_in": []string{"text"},
		"footer":    "caramba",
		"ts":        time.Now().Unix(),
	}
	if msg.Link != "" {
		attachment["title_link"] = msg.Link
	}
	return s.post(ctx, map[string]any{"attachments": []any{attachment}})
}

// truncate cuts text to at most max bytes, at a line break when one is
// near (so no formatting is left half open), else on a character boundary.
func truncate(text string, max int) string {
	if len(text) <= max {
		return text
	}
	if i := strings.LastIndexByte(text[:max], '\n'); i > max/2 {
		return text[:i] + "\n…(truncated)"
	}
	for max > 0 && !utf8.RuneStart(text[max]) {
		max--
	}
	return text[:max] + "\n…(truncated)"
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
		return fmt.Errorf("posting to %s: %w", w.service, unwrapURLError(err))
	}
	defer resp.Body.Close()
	// Slack answers 200, Teams workflows 202 Accepted.
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	err = fmt.Errorf("%s returned %d: %s", w.service, resp.StatusCode, strings.TrimSpace(string(detail)))
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
