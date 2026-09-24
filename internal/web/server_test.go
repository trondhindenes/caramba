package web

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/trondhindenes/caramba/internal/dispatch"
	"github.com/trondhindenes/caramba/internal/model"
	"github.com/trondhindenes/caramba/internal/notify"
	"github.com/trondhindenes/caramba/internal/repo"
	"github.com/trondhindenes/caramba/internal/store/localdir"
)

const testToken = "test-token"

func newTestServer(t *testing.T) (*httptest.Server, *repo.AlertRepo, *repo.TemplateRepo) {
	t.Helper()
	e := newTestEnv(t)
	return e.ts, e.alerts, e.templates
}

// testEnv is a full server wired to a fake "ops" destination.
type testEnv struct {
	ts         *httptest.Server
	alerts     *repo.AlertRepo
	templates  *repo.TemplateRepo
	rules      *repo.RuleRepo
	dispatches *repo.DispatchRepo
	dispatcher *dispatch.Dispatcher
	ops        *fakeDestination
}

type fakeDestination struct {
	mu   sync.Mutex
	sent []*notify.Message
}

func (f *fakeDestination) Send(_ context.Context, m *notify.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return nil
}

func (f *fakeDestination) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	st, err := localdir.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	e := &testEnv{
		alerts:     repo.NewAlertRepo(st),
		templates:  repo.NewTemplateRepo(st),
		rules:      repo.NewRuleRepo(st),
		dispatches: repo.NewDispatchRepo(st),
		ops:        &fakeDestination{},
	}
	dests, _ := notify.NewRegistry(nil, nil)
	dests.Add("ops", e.ops)
	e.dispatcher = dispatch.New(e.rules, e.templates, e.dispatches, dests, logger)
	// Cleanups run last-in first-out: close the server, then let background
	// routing finish before the temp dir is removed.
	t.Cleanup(e.dispatcher.Wait)
	e.ts = httptest.NewServer(NewHandler(Deps{
		WebhookToken: testToken,
		Alerts:       e.alerts,
		Templates:    e.templates,
		Rules:        e.rules,
		Dispatches:   e.dispatches,
		Dispatcher:   e.dispatcher,
		Destinations: dests.Names(),
		Logger:       logger,
	}))
	t.Cleanup(e.ts.Close)
	return e
}

func fixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/grafana-payload.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func postWebhook(t *testing.T, ts *httptest.Server, token string, body []byte) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+"/webhook", bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestWebhookAuth(t *testing.T) {
	ts, _, _ := newTestServer(t)
	if resp := postWebhook(t, ts, "", fixture(t)); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: want 401, got %d", resp.StatusCode)
	}
	if resp := postWebhook(t, ts, "wrong", fixture(t)); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong token: want 401, got %d", resp.StatusCode)
	}
}

func TestWebhookIngest(t *testing.T) {
	ts, alerts, _ := newTestServer(t)
	resp := postWebhook(t, ts, testToken, fixture(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	stored, _, err := alerts.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 {
		t.Fatalf("want 1 stored alert, got %d", len(stored))
	}
	if stored[0].Payload.CommonLabels["alertname"] != "HighCPU" {
		t.Errorf("unexpected payload: %+v", stored[0].Payload.CommonLabels)
	}
}

func TestWebhookRejectsBadJSON(t *testing.T) {
	ts, _, _ := newTestServer(t)
	if resp := postWebhook(t, ts, testToken, []byte("not json")); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

func TestAlertListPage(t *testing.T) {
	ts, alerts, _ := newTestServer(t)
	if _, err := alerts.Save(t.Context(), fixture(t), time.Now()); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "alertname=HighCPU") {
		t.Error("list page does not show the alert's group labels")
	}
}

func TestAlertListStatusFilter(t *testing.T) {
	ts, alerts, _ := newTestServer(t)
	if _, err := alerts.Save(t.Context(), fixture(t), time.Now()); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(ts.URL + "/?status=resolved")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "alertname=HighCPU") {
		t.Error("firing alert should be filtered out by status=resolved")
	}
}

func TestAlertDetailPage(t *testing.T) {
	ts, alerts, _ := newTestServer(t)
	saved, err := alerts.Save(t.Context(), fixture(t), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(ts.URL + "/alerts/" + saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	for _, want := range []string{"HighCPU", "web-1.example.com", "Raw payload"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("detail page missing %q", want)
		}
	}
}

func TestAlertDetailNotFound(t *testing.T) {
	ts, _, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/alerts/2026-06-10T00:00:00.000Z-NOPE")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

func postForm(t *testing.T, ts *httptest.Server, path string, form url.Values) *http.Response {
	t.Helper()
	resp, err := http.PostForm(ts.URL+path, form)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestTemplateCreateAndList(t *testing.T) {
	ts, _, templates := newTestServer(t)
	resp := postForm(t, ts, "/templates", url.Values{
		"name":   {"slack-default"},
		"engine": {"grafana"},
		"title":  {`[{{ .Status | toUpper }}] {{ .GroupLabels.alertname }}`},
		"body":   {`{{ .CommonAnnotations.summary }}`},
	})
	if resp.StatusCode != http.StatusOK { // after redirect to /templates
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	saved, err := templates.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || saved[0].Name != "slack-default" {
		t.Fatalf("template not saved: %+v", saved)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "slack-default") {
		t.Error("list page does not show saved template")
	}
}

func TestTemplateCreateRejectsBadSyntax(t *testing.T) {
	ts, _, templates := newTestServer(t)
	resp := postForm(t, ts, "/templates", url.Values{
		"name":   {"broken"},
		"engine": {"grafana"},
		"title":  {"ok"},
		"body":   {`{{ .Status`},
	})
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "body:") {
		t.Error("editor should re-render with the parse error")
	}
	saved, _ := templates.List(t.Context())
	if len(saved) != 0 {
		t.Error("invalid template must not be saved")
	}
}

func TestTemplateUpdateAndDelete(t *testing.T) {
	ts, _, templates := newTestServer(t)
	tmpl := &model.Template{Name: "n", Engine: "jinja2", Title: "t", Body: "b"}
	if err := templates.Save(t.Context(), tmpl); err != nil {
		t.Fatal(err)
	}

	postForm(t, ts, "/templates/"+tmpl.ID, url.Values{
		"name":   {"renamed"},
		"engine": {"jinja2"},
		"title":  {"t"},
		"body":   {"b"},
	})
	got, err := templates.Get(t.Context(), tmpl.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "renamed" {
		t.Errorf("update failed: %+v", got)
	}

	postForm(t, ts, "/templates/"+tmpl.ID+"/delete", url.Values{})
	if _, err := templates.Get(t.Context(), tmpl.ID); err == nil {
		t.Error("template should be deleted")
	}
}

func TestPreviewUnsavedTemplate(t *testing.T) {
	ts, alerts, _ := newTestServer(t)
	saved, err := alerts.Save(t.Context(), fixture(t), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	resp := postForm(t, ts, "/preview", url.Values{
		"alert_id": {saved.ID},
		"engine":   {"grafana"},
		"title":    {`[{{ .Status | toUpper }}] {{ .GroupLabels.alertname }}`},
		"body":     {`{{ range .Alerts.Firing }}{{ .Labels.instance }}: {{ .Annotations.summary }}{{ "\n" }}{{ end }}`},
	})
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{"[FIRING] HighCPU", "web-1.example.com: CPU usage above 90% for 5 minutes"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("preview missing %q in:\n%s", want, body)
		}
	}
}

func TestPreviewSavedTemplateJinja(t *testing.T) {
	ts, alerts, templates := newTestServer(t)
	saved, err := alerts.Save(t.Context(), fixture(t), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &model.Template{
		Name:   "jinja",
		Engine: "jinja2",
		Title:  `{{ status | upper }}: {{ groupLabels.alertname }}`,
		Body:   `{% for a in alerts %}{{ a.labels.instance }}{% if not loop.last %}, {% endif %}{% endfor %}`,
	}
	if err := templates.Save(t.Context(), tmpl); err != nil {
		t.Fatal(err)
	}
	resp := postForm(t, ts, "/preview", url.Values{
		"alert_id":    {saved.ID},
		"template_id": {tmpl.ID},
	})
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{"FIRING: HighCPU", "web-1.example.com, web-2.example.com"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("preview missing %q in:\n%s", want, body)
		}
	}
}

func TestPreviewRenderErrorShownInline(t *testing.T) {
	ts, alerts, _ := newTestServer(t)
	saved, err := alerts.Save(t.Context(), fixture(t), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	resp := postForm(t, ts, "/preview", url.Values{
		"alert_id": {saved.ID},
		"engine":   {"grafana"},
		"title":    {`{{ .NoSuchField.x }}`},
		"body":     {""},
	})
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "error-box") {
		t.Errorf("render error should appear in preview pane, got:\n%s", body)
	}
}

func TestPreviewFormats(t *testing.T) {
	ts, alerts, _ := newTestServer(t)
	saved, err := alerts.Save(t.Context(), fixture(t), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ format, body, want string }{
		{"raw", "*{{ .Status }}*", `<pre class="preview-body">*firing*</pre>`},
		{"slack", "*{{ .Status }}* <https://x.test|link>", `<strong>firing</strong> <a href="https://x.test">link</a>`},
		{"markdown", "**{{ .Status }}** [link](https://x.test)", `<strong>firing</strong> <a href="https://x.test">link</a>`},
	}
	for _, tc := range cases {
		t.Run(tc.format, func(t *testing.T) {
			resp := postForm(t, ts, "/preview", url.Values{
				"alert_id": {saved.ID},
				"engine":   {"grafana"},
				"title":    {"t"},
				"body":     {tc.body},
				"format":   {tc.format},
			})
			body, _ := io.ReadAll(resp.Body)
			if !strings.Contains(string(body), tc.want) {
				t.Errorf("want %q in:\n%s", tc.want, body)
			}
		})
	}
}

func (e *testEnv) template(t *testing.T) string {
	t.Helper()
	tm := &model.Template{Name: "slack", Engine: "jinja2", Title: "{{ status }}", Body: "b"}
	if err := e.templates.Save(t.Context(), tm); err != nil {
		t.Fatal(err)
	}
	return tm.ID
}

func (e *testEnv) ruleNames(t *testing.T) []string {
	t.Helper()
	rules, err := e.rules.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range rules {
		names = append(names, r.Name)
	}
	return names
}

func TestRuleCreateAndList(t *testing.T) {
	e := newTestEnv(t)
	tid := e.template(t)
	for _, f := range []url.Values{
		{"name": {"default"}, "template_id": {tid}, "destinations": {"ops"}},
		{"name": {"restarts"}, "template_id": {tid}, "matchers": {"title = *container restarts*"}},
	} {
		if resp := postForm(t, e.ts, "/rules", f); resp.StatusCode != http.StatusOK {
			t.Fatalf("create: %d", resp.StatusCode)
		}
	}
	rules, _ := e.rules.List(t.Context())
	if len(rules) != 2 || rules[1].Matchers[0].Pattern != "*container restarts*" || rules[0].Destinations[0] != "ops" {
		t.Fatalf("saved rules: %+v", rules)
	}

	resp, _ := http.Get(e.ts.URL + "/rules")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{"everything (default)", "title = *container restarts*", "never reached"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("rules page missing %q", want)
		}
	}

	postForm(t, e.ts, "/rules/"+rules[1].ID+"/move", url.Values{"direction": {"up"}})
	if got := e.ruleNames(t); got[0] != "restarts" {
		t.Errorf("after move up: %v", got)
	}
}

func TestRuleValidation(t *testing.T) {
	e := newTestEnv(t)
	tid := e.template(t)
	cases := map[string]url.Values{
		"name is required":    {"template_id": {tid}},
		"choose a template":   {"name": {"x"}},
		"matcher line 1":      {"name": {"x"}, "template_id": {tid}, "matchers": {"no equals sign"}},
		"unknown destination": {"name": {"x"}, "template_id": {tid}, "destinations": {"nope"}},
	}
	for want, form := range cases {
		resp := postForm(t, e.ts, "/rules", form)
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), want) {
			t.Errorf("want %q in error page", want)
		}
	}
	if got := e.ruleNames(t); len(got) != 0 {
		t.Errorf("invalid rules were saved: %v", got)
	}
}

func TestWebhookRoutesAlert(t *testing.T) {
	e := newTestEnv(t)
	e.rules.Save(t.Context(), &model.Rule{Name: "default", TemplateID: e.template(t), Destinations: []string{"ops"}})

	resp := postWebhook(t, e.ts, testToken, fixture(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("webhook: %d", resp.StatusCode)
	}
	e.dispatcher.Wait()
	if e.ops.count() != 1 {
		t.Fatalf("want 1 sent message, got %d", e.ops.count())
	}

	stored, _, _ := e.alerts.List(t.Context())
	detail, _ := http.Get(e.ts.URL + "/alerts/" + stored[0].ID)
	body, _ := io.ReadAll(detail.Body)
	detail.Body.Close()
	if !strings.Contains(string(body), "sent") || !strings.Contains(string(body), "default") {
		t.Errorf("alert page should show routing outcome:\n%s", body)
	}
}

func TestRuleTestSend(t *testing.T) {
	e := newTestEnv(t)
	rule := &model.Rule{Name: "restarts", TemplateID: e.template(t), Destinations: []string{"ops"},
		Matchers: []model.Matcher{{Field: "title", Pattern: "*container restarts*"}}}
	e.rules.Save(t.Context(), rule)
	alert, _ := e.alerts.Save(t.Context(), fixture(t), time.Now())

	resp := postForm(t, e.ts, "/rules/"+rule.ID+"/test", url.Values{"alert_id": {alert.ID}})
	body, _ := io.ReadAll(resp.Body)
	if e.ops.count() != 1 {
		t.Fatalf("test send should deliver even without a match, sent %d", e.ops.count())
	}
	for _, want := range []string{"does not match", "sent"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("result missing %q:\n%s", want, body)
		}
	}
	if _, err := e.dispatches.Get(t.Context(), alert.ID); err == nil {
		t.Error("test sends must not overwrite the alert's routing record")
	}
}
