package format

import (
	"reflect"
	"strings"
	"testing"
)

func TestSlack(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"bold", "*sartor-loom-api* in", "<strong>sartor-loom-api</strong> in"},
		{"italic", "was _slow_ today", "was <em>slow</em> today"},
		{"strike", "~gone~", "<del>gone</del>"},
		{"inline code", "in `gke-npd/staging`", "in <code>gke-npd/staging</code>"},
		{"code keeps markers", "`*not bold*`", "<code>*not bold*</code>"},
		{"code block", "```a *b*\nc```", "<pre>a *b*\nc</pre>"},
		{"labelled link", "<https://x.test/a?b=1&c=2|View rule>", `<a href="https://x.test/a?b=1&amp;c=2">View rule</a>`},
		{"bare link", "<https://x.test>", `<a href="https://x.test">https://x.test</a>`},
		{"link url keeps underscores", "<https://x.test/__a_b__|x>", `<a href="https://x.test/__a_b__">x</a>`},
		{"bold around link", "*<https://x.test|go>*", `<strong><a href="https://x.test">go</a></strong>`},
		{"snake_case untouched", "rule_uid_value", "rule_uid_value"},
		{"lone asterisk", "2 * 3 = 6", "2 * 3 = 6"},
		{"adjacent emphasis", "*a* *b* _c_ _d_", "<strong>a</strong> <strong>b</strong> <em>c</em> <em>d</em>"},
		{"nested emphasis", "*_both_*", "<strong><em>both</em></strong>"},
		{"multiline", "*a*\n\n`b`\n", "<strong>a</strong>\n\n<code>b</code>\n"},
		{"forged placeholder", "\x000\x00 `x`", "0 <code>x</code>"},
		{"escapes html", "<b>hi</b> & <script>", "&lt;b&gt;hi&lt;/b&gt; &amp; &lt;script&gt;"},
		{"unsafe scheme not linked", "<javascript:alert(1)|x>", "&lt;javascript:alert(1)|x&gt;"},
		{"escapes link label", "<https://x.test|<img>>", `<a href="https://x.test">&lt;img</a>&gt;`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Render(Slack, tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestParseSlack(t *testing.T) {
	got := ParseSlack("*Alert* <https://x.test|_open_>\n~old~ `code`\n```\nline 1\n  line 2```tail")
	want := []Block{
		{Lines: [][]Span{
			{{Text: "Alert", Bold: true}, {Text: " "}, {Text: "_open_", URL: "https://x.test"}},
			{{Text: "old", Strike: true}, {Text: " "}, {Text: "code", Code: true}},
			nil,
		}},
		{Pre: true, Text: "line 1\n  line 2"},
		{Lines: [][]Span{{{Text: "tail"}}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestMarkdown(t *testing.T) {
	got, err := Render(Markdown, "**bold** and [link](https://x.test)\nnext line\n\n<script>alert(1)</script>\n\n[bad](javascript:alert(1))")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<strong>bold</strong>", `<a href="https://x.test">link</a>`, "<br"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	for _, bad := range []string{"<script", "javascript:"} {
		if strings.Contains(string(got), bad) {
			t.Errorf("unsafe %q rendered in %s", bad, got)
		}
	}
}

func TestRawEscapes(t *testing.T) {
	got, _ := Render(Raw, "<b>*x*</b>")
	if string(got) != "&lt;b&gt;*x*&lt;/b&gt;" {
		t.Errorf("raw: %q", got)
	}
	if _, err := Render("html", "x"); err == nil {
		t.Error("unknown format should error")
	}
}
