package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/trondhindenes/caramba/internal/config"
	"github.com/trondhindenes/caramba/internal/engine"
)

// slackServer answers with the given statuses in turn (repeating the last)
// and keeps the last request body.
func slackServer(t *testing.T, statuses ...int) (*httptest.Server, *atomic.Int32, *atomic.Value) {
	t.Helper()
	var calls atomic.Int32
	var lastBody atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1))
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		lastBody.Store(body)
		status := statuses[min(n, len(statuses))-1]
		w.WriteHeader(status)
		if status != http.StatusOK {
			w.Write([]byte("oops"))
		}
	}))
	t.Cleanup(ts.Close)
	return ts, &calls, &lastBody
}

func registry(url string) *Registry {
	return registryWith(url, "slack", nil)
}

func registryWith(url, typ string, colors []config.ColorRule) *Registry {
	r, err := NewRegistry([]config.Destination{{Name: "ops", Type: typ, WebhookURL: url}}, colors)
	if err != nil {
		panic(err)
	}
	r.Backoff = []time.Duration{time.Millisecond, time.Millisecond}
	return r
}

var msg = &Message{
	Rendered:   engine.Rendered{Title: "🔴 api down", Body: "*api* in `prod`\n"},
	Status:     "firing",
	AlertTitle: "[FIRING:1] api down (staging)",
	Link:       "https://grafana.test/rule/1",
}

func TestSlackSendsTitleAndBody(t *testing.T) {
	ts, calls, body := slackServer(t, 200)
	attempts, err := registry(ts.URL).Send(context.Background(), "ops", msg)
	if err != nil || attempts != 1 || calls.Load() != 1 {
		t.Fatalf("attempts=%d calls=%d err=%v", attempts, calls.Load(), err)
	}
	if got := body.Load().(map[string]any)["text"]; got != "*🔴 api down*\n*api* in `prod`" {
		t.Errorf("text: %q", got)
	}
}

func TestSlackAttachment(t *testing.T) {
	ts, _, body := slackServer(t, 200)
	if _, err := registryWith(ts.URL, "slack-attachment", nil).Send(context.Background(), "ops", msg); err != nil {
		t.Fatal(err)
	}
	a := body.Load().(map[string]any)["attachments"].([]any)[0].(map[string]any)
	want := map[string]any{
		"title":      "🔴 api down",
		"title_link": "https://grafana.test/rule/1",
		"text":       "*api* in `prod`",
		"color":      colorFiring,
		"footer":     "caramba",
	}
	for k, v := range want {
		if a[k] != v {
			t.Errorf("%s: got %v, want %v", k, a[k], v)
		}
	}
}

func TestAttachmentColors(t *testing.T) {
	rules := []config.ColorRule{
		{Title: "*critical*", Color: "danger"},
		{Title: "*(staging)*", Color: "#FFA500"},
	}
	cases := []struct{ status, title, want string }{
		{"firing", "[FIRING:1] disk critical (prod)", "danger"},
		{"firing", "[FIRING:1] api down (STAGING)", "#FFA500"}, // case-insensitive
		{"firing", "[FIRING:1] api down (prod)", colorFiring},
		{"resolved", "[RESOLVED] api down (prod)", colorResolved},
		{"", "unknown", ""},
	}
	for _, tc := range cases {
		ts, _, body := slackServer(t, 200)
		m := *msg
		m.Status, m.AlertTitle = tc.status, tc.title
		if _, err := registryWith(ts.URL, "slack-attachment", rules).Send(context.Background(), "ops", &m); err != nil {
			t.Fatal(err)
		}
		a := body.Load().(map[string]any)["attachments"].([]any)[0].(map[string]any)
		if a["color"] != tc.want {
			t.Errorf("%q: color %v, want %q", tc.title, a["color"], tc.want)
		}
	}
}

// teamsCard sends msg to a teams destination and returns the posted card.
func teamsCard(t *testing.T, m *Message, rules []config.ColorRule) map[string]any {
	t.Helper()
	ts, _, body := slackServer(t, http.StatusAccepted)
	if _, err := registryWith(ts.URL, "teams", rules).Send(context.Background(), "ops", m); err != nil {
		t.Fatal(err)
	}
	payload := body.Load().(map[string]any)
	a := payload["attachments"].([]any)[0].(map[string]any)
	if payload["type"] != "message" || a["contentType"] != "application/vnd.microsoft.card.adaptive" {
		t.Fatalf("not a card message: %v", payload)
	}
	return a["content"].(map[string]any)
}

func TestTeamsCard(t *testing.T) {
	m := *msg
	m.Body = "*api* in `prod` is _down_ ~maybe~\n<https://x.test|runbook>\n\n```\n  pod-1\n```\nsnake_case_id"
	card := teamsCard(t, &m, nil)
	got, _ := json.Marshal(card["body"])
	want := `[` +
		`{"bleed":true,"items":[{"inlines":[{"selectAction":{"type":"Action.OpenUrl","url":"https://grafana.test/rule/1"},"size":"Large","text":"🔴 api down","type":"TextRun","weight":"Bolder"}],"type":"RichTextBlock"}],"style":"attention","type":"Container"},` +
		`{"inlines":[{"text":"api","type":"TextRun","weight":"Bolder"},{"text":" in ","type":"TextRun"},{"fontType":"Monospace","text":"prod","type":"TextRun"},{"text":" is ","type":"TextRun"},{"italic":true,"text":"down","type":"TextRun"},{"text":" ","type":"TextRun"},{"strikethrough":true,"text":"maybe","type":"TextRun"}],"spacing":"Medium","type":"RichTextBlock"},` +
		`{"inlines":[{"selectAction":{"type":"Action.OpenUrl","url":"https://x.test"},"text":"runbook","type":"TextRun"}],"spacing":"None","type":"RichTextBlock"},` +
		`{"items":[{"inlines":[{"fontType":"Monospace","text":"` + "\u00a0\u00a0" + `pod-1","type":"TextRun"}],"spacing":"None","type":"RichTextBlock"}],"spacing":"Medium","style":"emphasis","type":"Container"},` +
		`{"inlines":[{"text":"snake_case_id","type":"TextRun"}],"spacing":"Small","type":"RichTextBlock"}` +
		`]`
	if string(got) != want {
		t.Errorf("body:\n got %s\nwant %s", got, want)
	}
	actions, _ := json.Marshal(card["actions"])
	if string(actions) != `[{"title":"View in Grafana","type":"Action.OpenUrl","url":"https://grafana.test/rule/1"}]` {
		t.Errorf("actions: %s", actions)
	}
}

func TestTeamsWithoutLink(t *testing.T) {
	m := *msg
	m.Link = ""
	card := teamsCard(t, &m, nil)
	if _, ok := card["actions"]; ok {
		t.Error("no link should mean no button")
	}
}

func TestTeamsStyles(t *testing.T) {
	rules := []config.ColorRule{
		{Title: "*warn*", Color: "warning"},
		{Title: "*orange*", Color: "#FFA500"},
		{Title: "*blue*", Color: "#1F77B4"},
		{Title: "*gray*", Color: "#808080"},
	}
	cases := []struct{ status, title, want string }{
		{"firing", "x", "attention"},
		{"resolved", "x", "good"},
		{"", "x", "default"},
		{"firing", "warn", "warning"},
		{"firing", "orange", "warning"},
		{"firing", "blue", "accent"},
		{"firing", "gray", "emphasis"},
	}
	for _, tc := range cases {
		m := *msg
		m.Status, m.AlertTitle = tc.status, tc.title
		header := teamsCard(t, &m, rules)["body"].([]any)[0].(map[string]any)
		if header["style"] != tc.want {
			t.Errorf("%s/%s: style %v, want %s", tc.status, tc.title, header["style"], tc.want)
		}
	}
}

func TestTeamsFitsSizeLimit(t *testing.T) {
	m := *msg
	m.Body = strings.Repeat("*a* `b` ", 5000)
	var size int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		size = len(b)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)
	if _, err := registryWith(srv.URL, "teams", nil).Send(context.Background(), "ops", &m); err != nil {
		t.Fatal(err)
	}
	if size == 0 || size > maxTeamsPayload {
		t.Errorf("payload is %d bytes", size)
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"short", 10, "short"},
		{"line one *a*\nline two *b*", 20, "line one *a*\n…(truncated)"},
		{"no line breaks at all", 8, "no line \n…(truncated)"},
		{"ab€cd", 4, "ab\n…(truncated)"}, // never splits a character
	}
	for _, tc := range cases {
		if got := truncate(tc.in, tc.max); got != tc.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
		}
	}
}

func TestRetriesTransientFailures(t *testing.T) {
	ts, calls, _ := slackServer(t, 500, 429, 200)
	attempts, err := registry(ts.URL).Send(context.Background(), "ops", msg)
	if err != nil || attempts != 3 || calls.Load() != 3 {
		t.Fatalf("attempts=%d calls=%d err=%v", attempts, calls.Load(), err)
	}
}

func TestGivesUpAfterBackoff(t *testing.T) {
	ts, calls, _ := slackServer(t, 503)
	attempts, err := registry(ts.URL).Send(context.Background(), "ops", msg)
	if err == nil || attempts != 3 || calls.Load() != 3 {
		t.Fatalf("attempts=%d calls=%d err=%v", attempts, calls.Load(), err)
	}
}

func TestNoRetryOnClientError(t *testing.T) {
	ts, calls, _ := slackServer(t, 404)
	attempts, err := registry(ts.URL).Send(context.Background(), "ops", msg)
	if err == nil || !strings.Contains(err.Error(), "404") || attempts != 1 || calls.Load() != 1 {
		t.Fatalf("attempts=%d calls=%d err=%v", attempts, calls.Load(), err)
	}
}

func TestErrorsHideWebhookURL(t *testing.T) {
	secret := "http://127.0.0.1:1/services/SECRET"
	_, err := registry(secret).Send(context.Background(), "ops", msg)
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("error must not leak the URL: %v", err)
	}
}

func TestUnknownDestination(t *testing.T) {
	_, err := registry("http://x").Send(context.Background(), "nope", msg)
	if err == nil || !strings.Contains(err.Error(), "unknown destination") {
		t.Fatalf("got %v", err)
	}
}
