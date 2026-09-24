// Package format renders templated message text the way a destination
// would display it, so previews show the message as recipients will see
// it. Every renderer escapes the input, so template output (which carries
// alert content from monitored systems) can never inject markup.
package format

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"regexp"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	gmhtml "github.com/yuin/goldmark/renderer/html"
)

const (
	Raw      = "raw"
	Slack    = "slack"
	Markdown = "markdown"
)

// Names returns the supported formats in stable order for GUI menus.
func Names() []string { return []string{Raw, Slack, Markdown} }

// Render converts text in the given format to HTML. Raw returns the text
// escaped, for display in a preformatted block.
func Render(format, text string) (template.HTML, error) {
	switch format {
	case Raw, "":
		return template.HTML(html.EscapeString(text)), nil
	case Slack:
		return renderSlack(text), nil
	case Markdown:
		return renderMarkdown(text)
	default:
		return "", fmt.Errorf("unknown format %q", format)
	}
}

// Goldmark's default renderer drops raw HTML and unsafe link schemes. Hard
// wraps match chat destinations, where a single newline breaks the line.
var markdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(gmhtml.WithHardWraps()),
)

func renderMarkdown(text string) (template.HTML, error) {
	var buf bytes.Buffer
	if err := markdown.Convert([]byte(text), &buf); err != nil {
		return "", fmt.Errorf("rendering markdown: %w", err)
	}
	return template.HTML(buf.String()), nil
}

var (
	slackCodeBlock = regexp.MustCompile("(?s)```\n?(.*?)```")
	slackCode      = regexp.MustCompile("`([^`\n]+)`")
	slackLink      = regexp.MustCompile(`<((?:https?://|mailto:)[^|>\s]+)(?:\|([^>]*))?>`)
	// Emphasis markers only count at word boundaries, as in Slack, so
	// snake_case identifiers stay intact.
	slackBold   = regexp.MustCompile(`(^|[^\w*])\*(\S(?:[^*\n]*?\S)?)\*($|[^\w*])`)
	slackItalic = regexp.MustCompile(`(^|[^\w_])_(\S(?:[^_\n]*?\S)?)_($|[^\w_])`)
	slackStrike = regexp.MustCompile(`(^|[^\w~])~(\S(?:[^~\n]*?\S)?)~($|[^\w~])`)
	placeholder = regexp.MustCompile("\x00(\\d+)\x00")
)

// renderSlack converts Slack mrkdwn to HTML. Code and links are swapped for
// placeholders first so their contents escape formatting, then the rest is
// escaped and emphasis applied.
func renderSlack(text string) template.HTML {
	var tokens []string
	stash := func(s string) string {
		tokens = append(tokens, s)
		return "\x00" + strconv.Itoa(len(tokens)-1) + "\x00"
	}
	text = strings.ReplaceAll(text, "\x00", "")
	text = slackCodeBlock.ReplaceAllStringFunc(text, func(m string) string {
		return stash("<pre>" + html.EscapeString(slackCodeBlock.FindStringSubmatch(m)[1]) + "</pre>")
	})
	text = slackCode.ReplaceAllStringFunc(text, func(m string) string {
		return stash("<code>" + html.EscapeString(slackCode.FindStringSubmatch(m)[1]) + "</code>")
	})
	text = slackLink.ReplaceAllStringFunc(text, func(m string) string {
		parts := slackLink.FindStringSubmatch(m)
		label := parts[2]
		if label == "" {
			label = parts[1]
		}
		return stash(`<a href="` + html.EscapeString(parts[1]) + `">` + html.EscapeString(label) + "</a>")
	})
	text = html.EscapeString(text)
	text = slackBold.ReplaceAllString(text, "$1<strong>$2</strong>$3")
	text = slackItalic.ReplaceAllString(text, "$1<em>$2</em>$3")
	text = slackStrike.ReplaceAllString(text, "$1<del>$2</del>$3")
	text = placeholder.ReplaceAllStringFunc(text, func(m string) string {
		i, _ := strconv.Atoi(placeholder.FindStringSubmatch(m)[1])
		return tokens[i]
	})
	return template.HTML(text)
}
