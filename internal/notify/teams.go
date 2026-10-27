package notify

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/trondhindenes/caramba/internal/format"
)

const (
	// maxTeamsPayload stays under Teams' ~28 KB message limit.
	maxTeamsPayload = 26000
	// maxTeamsText is the first body budget tried; styled text costs
	// several times its length in card JSON, so Send shrinks the budget in
	// proportion to the overshoot until the card fits.
	maxTeamsText = 12000
)

// Teams posts an Adaptive Card to a Teams Workflows webhook ("Post to a
// channel when a webhook request is received"). The body, Slack mrkdwn
// like for every destination, is converted to formatted text runs, so it
// shows as the Slack preview does. The title sits in a band tinted by the
// alert's color.
type Teams struct {
	webhook
	colors *colorizer
}

func NewTeams(webhookURL string, colors *colorizer) *Teams {
	return &Teams{webhook: newWebhook("teams", webhookURL), colors: colors}
}

func (t *Teams) Send(ctx context.Context, msg *Message) error {
	style := cardStyle(t.colors.color(msg))
	limit := maxTeamsText
	for {
		payload := teamsMessage(msg, style, limit)
		body, err := json.Marshal(payload)
		if err != nil || len(body) <= maxTeamsPayload || limit < 500 {
			return t.post(ctx, payload)
		}
		// 90% leaves room for the title, actions and a ragged cut.
		limit = limit * maxTeamsPayload / len(body) * 9 / 10
	}
}

// teamsMessage builds the webhook payload: a message carrying one card,
// with the body cut to limit bytes.
func teamsMessage(msg *Message, style string, limit int) map[string]any {
	title := strings.TrimSpace(msg.Title)
	titleRun := map[string]any{"type": "TextRun", "text": title, "size": "Large", "weight": "Bolder"}
	if msg.Link != "" {
		titleRun["selectAction"] = openURL(msg.Link)
	}
	body := []any{map[string]any{
		"type":  "Container",
		"style": style,
		"bleed": true,
		"items": []any{richText(titleRun)},
	}}
	body = append(body, cardBody(truncate(strings.TrimSpace(msg.Body), limit))...)

	card := map[string]any{
		"$schema":      "http://adaptivecards.io/schemas/adaptive-card.json",
		"type":         "AdaptiveCard",
		"version":      "1.4",
		"fallbackText": title,
		"msteams":      map[string]any{"width": "Full"},
		"body":         body,
	}
	if msg.Link != "" {
		card["actions"] = []any{map[string]any{"type": "Action.OpenUrl", "title": "View in Grafana", "url": msg.Link}}
	}
	return map[string]any{
		"type": "message",
		"attachments": []any{map[string]any{
			"contentType": "application/vnd.microsoft.card.adaptive",
			"contentUrl":  nil,
			"content":     card,
		}},
	}
}

// cardBody converts Slack mrkdwn to card elements: one RichTextBlock per
// line, because Teams is inconsistent about line breaks inside a block,
// and a shaded container per code block. Blank lines become spacing. Text
// runs are never parsed as Markdown, so alert content shows verbatim.
func cardBody(text string) []any {
	var items []any
	gap := true // the first item sits apart from the title
	spacing := func(tight string) string {
		if gap {
			gap = false
			return "Medium"
		}
		return tight
	}
	blocks := format.ParseSlack(text)
	for bi, block := range blocks {
		if block.Pre {
			var lines []any
			for _, line := range strings.Split(strings.Trim(block.Text, "\n"), "\n") {
				run := map[string]any{"type": "TextRun", "text": keepIndent(line), "fontType": "Monospace"}
				lines = append(lines, withSpacing(richText(run), "None"))
			}
			items = append(items, map[string]any{
				"type":    "Container",
				"style":   "emphasis",
				"spacing": spacing("Small"),
				"items":   lines,
			})
			continue
		}
		lines := block.Lines
		// Next to a code block, the first or last line is just the rest of
		// the fence's line, not a blank line.
		if bi > 0 && len(lines) > 0 && blank(lines[0]) {
			lines = lines[1:]
		}
		if bi < len(blocks)-1 && len(lines) > 0 && blank(lines[len(lines)-1]) {
			lines = lines[:len(lines)-1]
		}
		tight := "None"
		if bi > 0 {
			tight = "Small" // clear of the code block above
		}
		for _, line := range lines {
			if blank(line) {
				gap = true
				continue
			}
			runs := make([]any, len(line))
			for i, s := range line {
				if i == 0 {
					s.Text = keepIndent(s.Text)
				}
				runs[i] = textRun(s)
			}
			items = append(items, withSpacing(richText(runs...), spacing(tight)))
			tight = "None"
		}
	}
	return items
}

func textRun(s format.Span) map[string]any {
	run := map[string]any{"type": "TextRun", "text": s.Text}
	if s.Bold {
		run["weight"] = "Bolder"
	}
	if s.Italic {
		run["italic"] = true
	}
	if s.Strike {
		run["strikethrough"] = true
	}
	if s.Code {
		run["fontType"] = "Monospace"
	}
	if s.URL != "" {
		run["selectAction"] = openURL(s.URL)
	}
	return run
}

func richText(runs ...any) map[string]any {
	return map[string]any{"type": "RichTextBlock", "inlines": runs}
}

func withSpacing(item map[string]any, spacing string) map[string]any {
	item["spacing"] = spacing
	return item
}

func openURL(url string) map[string]any {
	return map[string]any{"type": "Action.OpenUrl", "url": url}
}

// blank reports whether a line has no visible text.
func blank(line []format.Span) bool {
	for _, s := range line {
		if strings.TrimSpace(s.Text) != "" {
			return false
		}
	}
	return true
}

// keepIndent swaps leading spaces and tabs for non-breaking spaces, which
// Teams doesn't collapse; an empty line becomes one so it keeps its height.
func keepIndent(s string) string {
	rest := strings.TrimLeft(s, " \t")
	indent := s[:len(s)-len(rest)]
	indent = strings.ReplaceAll(indent, "\t", "    ")
	if rest == "" && indent == "" {
		return " "
	}
	return strings.Repeat(" ", len(indent)) + rest
}
