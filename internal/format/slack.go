package format

import (
	"html"
	"html/template"
	"regexp"
	"strconv"
	"strings"
)

// Span is a run of Slack mrkdwn text with uniform styling.
type Span struct {
	Text                       string
	Bold, Italic, Strike, Code bool
	// URL is set when the span is a link.
	URL string
}

// Block is part of a Slack mrkdwn message: a code block, or lines of styled
// text.
type Block struct {
	Pre bool
	// Text is the code block's contents, when Pre.
	Text string
	// Lines holds the text's lines otherwise; an empty line is a blank one.
	Lines [][]Span
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
	// Control characters used below as placeholders and style markers;
	// stripped from the input so it can't forge them.
	controlChars = strings.NewReplacer("\x00", "", "\x01", "", "\x02", "", "\x03", "", "\x04", "", "\x05", "", "\x06", "")
)

// ParseSlack splits Slack mrkdwn into code blocks and styled text, so every
// destination and the preview interpret a template's output the same way.
func ParseSlack(text string) []Block {
	text = controlChars.Replace(text)
	var blocks []Block
	addText := func(s string) {
		if s != "" {
			blocks = append(blocks, Block{Lines: parseInline(s)})
		}
	}
	last := 0
	for _, m := range slackCodeBlock.FindAllStringSubmatchIndex(text, -1) {
		addText(text[last:m[0]])
		blocks = append(blocks, Block{Pre: true, Text: text[m[2]:m[3]]})
		last = m[1]
	}
	addText(text[last:])
	return blocks
}

// parseInline styles text without code blocks. Inline code and links are
// swapped for placeholders first so their contents escape formatting, then
// emphasis is marked with control characters and the result split into
// spans.
func parseInline(text string) [][]Span {
	var stashed []Span
	stash := func(s Span) string {
		stashed = append(stashed, s)
		return "\x00" + strconv.Itoa(len(stashed)-1) + "\x00"
	}
	text = slackCode.ReplaceAllStringFunc(text, func(m string) string {
		return stash(Span{Text: slackCode.FindStringSubmatch(m)[1], Code: true})
	})
	text = slackLink.ReplaceAllStringFunc(text, func(m string) string {
		parts := slackLink.FindStringSubmatch(m)
		label := parts[2]
		if label == "" {
			label = parts[1]
		}
		return stash(Span{Text: strings.ReplaceAll(label, "\n", " "), URL: parts[1]})
	})
	text = markAll(slackBold, text, "\x01", "\x02")
	text = markAll(slackItalic, text, "\x03", "\x04")
	text = markAll(slackStrike, text, "\x05", "\x06")

	lines := [][]Span{nil}
	var style Span
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			s := style
			s.Text = buf.String()
			lines[len(lines)-1] = append(lines[len(lines)-1], s)
			buf.Reset()
		}
	}
	for i := 0; i < len(text); i++ {
		switch c := text[i]; c {
		case '\x01', '\x02':
			flush()
			style.Bold = c == '\x01'
		case '\x03', '\x04':
			flush()
			style.Italic = c == '\x03'
		case '\x05', '\x06':
			flush()
			style.Strike = c == '\x05'
		case '\n':
			flush()
			lines = append(lines, nil)
		case '\x00':
			flush()
			end := i + 1 + strings.IndexByte(text[i+1:], 0)
			n, _ := strconv.Atoi(text[i+1 : end])
			s := stashed[n]
			s.Bold, s.Italic, s.Strike = style.Bold, style.Italic, style.Strike
			lines[len(lines)-1] = append(lines[len(lines)-1], s)
			i = end
		default:
			buf.WriteByte(c)
		}
	}
	flush()
	return lines
}

// markAll wraps every match's content in open/close markers. A match
// consumes the character after it, which may be the one the next match
// needs before it ("*a* *b*"), so it repeats until nothing changes.
func markAll(re *regexp.Regexp, text, open, close string) string {
	for {
		next := re.ReplaceAllString(text, "${1}"+open+"${2}"+close+"${3}")
		if next == text {
			return text
		}
		text = next
	}
}

// renderSlack converts Slack mrkdwn to HTML.
func renderSlack(text string) template.HTML {
	var b strings.Builder
	for _, block := range ParseSlack(text) {
		if block.Pre {
			b.WriteString("<pre>" + html.EscapeString(block.Text) + "</pre>")
			continue
		}
		for i, line := range block.Lines {
			if i > 0 {
				b.WriteByte('\n')
			}
			for _, s := range line {
				b.WriteString(spanHTML(s))
			}
		}
	}
	return template.HTML(b.String())
}

func spanHTML(s Span) string {
	out := html.EscapeString(s.Text)
	if s.Code {
		out = "<code>" + out + "</code>"
	}
	if s.URL != "" {
		out = `<a href="` + html.EscapeString(s.URL) + `">` + out + "</a>"
	}
	if s.Strike {
		out = "<del>" + out + "</del>"
	}
	if s.Italic {
		out = "<em>" + out + "</em>"
	}
	if s.Bold {
		out = "<strong>" + out + "</strong>"
	}
	return out
}
