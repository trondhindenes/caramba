package web

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/trondhindenes/caramba/internal/config"
	"github.com/trondhindenes/caramba/internal/model"
	"github.com/trondhindenes/caramba/internal/repo"
	"github.com/trondhindenes/caramba/internal/store/localdir"
)

const testToken = "test-token"

func newTestServer(t *testing.T) (*httptest.Server, *repo.AlertRepo, *repo.TemplateRepo) {
	t.Helper()
	st, err := localdir.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	alerts := repo.NewAlertRepo(st)
	templates := repo.NewTemplateRepo(st)
	cfg := &config.Config{WebhookToken: testToken}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ts := httptest.NewServer(NewHandler(cfg, alerts, templates, logger))
	t.Cleanup(ts.Close)
	return ts, alerts, templates
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
