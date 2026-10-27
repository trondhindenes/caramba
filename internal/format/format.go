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
