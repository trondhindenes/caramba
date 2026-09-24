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

	"github.com/trondhindenes/caramba/internal/engine"
)

// maxSlackText stays under Slack's 40k character limit for message text.
const maxSlackText = 39000

// Slack posts to a Slack incoming webhook. The title is sent in bold above
// the body; both are Slack mrkdwn.
type Slack struct {
	url    string
	client *http.Client
}

func NewSlack(webhookURL string) *Slack {
	return &Slack{url: webhookURL, client: &http.Client{Timeout: 15 * time.Second}}
}

func (s *Slack) Send(ctx context.Context, msg *engine.Rendered) error {
	text := strings.TrimSpace(msg.Body)
	if title := strings.TrimSpace(msg.Title); title != "" {
		text = "*" + title + "*\n" + text
	}
	if len(text) > maxSlackText {
		text = text[:maxSlackText] + "\n…(truncated)"
	}
	payload, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return permanentError{err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(payload))
	if err != nil {
		return permanentError{err}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
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
