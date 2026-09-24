package mcpserver

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trondhindenes/caramba/internal/model"
	"github.com/trondhindenes/caramba/internal/repo"
	"github.com/trondhindenes/caramba/internal/store/localdir"
)

const testToken = "mcp-test-token"

type fixtures struct {
	alerts    *repo.AlertRepo
	templates *repo.TemplateRepo
	rules     *repo.RuleRepo
}

func newRepos(t *testing.T) fixtures {
	t.Helper()
	st, err := localdir.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return fixtures{alerts: repo.NewAlertRepo(st), templates: repo.NewTemplateRepo(st), rules: repo.NewRuleRepo(st)}
}

func (f fixtures) saveAlert(t *testing.T, raw []byte) string {
	t.Helper()
	a, err := f.alerts.Save(t.Context(), raw, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return a.ID
}

func grafanaFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/grafana-payload.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// connect runs the server over the real HTTP transport, so the auth wrapper
// and stateless mode are exercised too.
func connect(t *testing.T, f fixtures) *mcp.ClientSession {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ts := httptest.NewServer(NewHandler(f.alerts, f.templates, f.rules, testToken, logger))
	t.Cleanup(ts.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	cs, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   ts.URL,
		HTTPClient: &http.Client{Transport: bearer{testToken}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// call invokes a tool and decodes its structured output into out. It fails
// the test on tool errors unless wantErr is set, in which case it returns
// the error text.
func call(t *testing.T, cs *mcp.ClientSession, name string, args any, out any) string {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		return res.Content[0].(*mcp.TextContent).Text
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("%s: decoding %s: %v", name, raw, err)
	}
	return ""
}

func TestRequiresToken(t *testing.T) {
	f := newRepos(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ts := httptest.NewServer(NewHandler(f.alerts, f.templates, f.rules, testToken, logger))
	defer ts.Close()
	for _, auth := range []string{"", "Bearer wrong"} {
		req, _ := http.NewRequest("POST", ts.URL, strings.NewReader("{}"))
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("auth %q: want 401, got %d", auth, resp.StatusCode)
		}
	}
}

func TestListAndGetAlerts(t *testing.T) {
	f := newRepos(t)
	firingID := f.saveAlert(t, grafanaFixture(t))
	// A sparse payload: Grafana omits maps, which must not break output.
	f.saveAlert(t, []byte(`{"status":"resolved","receiver":"r"}`))
	cs := connect(t, f)

	var all listAlertsOutput
	if msg := call(t, cs, "list_alerts", map[string]any{}, &all); msg != "" {
		t.Fatal(msg)
	}
	if len(all.Alerts) != 2 || all.More {
		t.Fatalf("want 2 alerts and no more, got %+v", all)
	}

	var firing listAlertsOutput
	call(t, cs, "list_alerts", map[string]any{"status": "firing"}, &firing)
	if len(firing.Alerts) != 1 || firing.Alerts[0].ID != firingID {
		t.Fatalf("status filter: %+v", firing)
	}
	if got := firing.Alerts[0].AlertNames; len(got) != 1 || got[0] != "HighCPU" {
		t.Errorf("alert names: %v", got)
	}

	var limited listAlertsOutput
	call(t, cs, "list_alerts", map[string]any{"limit": 1}, &limited)
	if len(limited.Alerts) != 1 || !limited.More {
		t.Errorf("limit: %+v", limited)
	}

	var got struct {
		ID      string        `json:"id"`
		Payload model.Payload `json:"payload"`
	}
	if msg := call(t, cs, "get_alert", map[string]any{"id": firingID}, &got); msg != "" {
		t.Fatal(msg)
	}
	if got.ID != firingID || got.Payload.CommonLabels["alertname"] != "HighCPU" {
		t.Errorf("get_alert: %+v", got)
	}

	if msg := call(t, cs, "get_alert", map[string]any{"id": "nope"}, nil); !strings.Contains(msg, "no alert") {
		t.Errorf("missing alert: want not-found error, got %q", msg)
	}
}

func TestTemplates(t *testing.T) {
	f := newRepos(t)
	tmpl := &model.Template{Name: "cpu", Engine: "grafana", Title: "[{{ .Status }}]", Body: "b"}
	if err := f.templates.Save(t.Context(), tmpl); err != nil {
		t.Fatal(err)
	}
	cs := connect(t, f)

	var list listTemplatesOutput
	call(t, cs, "list_templates", map[string]any{}, &list)
	if len(list.Templates) != 1 || list.Templates[0].ID != tmpl.ID {
		t.Fatalf("list_templates: %+v", list)
	}
	var got model.Template
	call(t, cs, "get_template", map[string]any{"id": tmpl.ID}, &got)
	if got.Title != tmpl.Title {
		t.Errorf("get_template: %+v", got)
	}
}

func TestPreview(t *testing.T) {
	f := newRepos(t)
	alertID := f.saveAlert(t, grafanaFixture(t))
	saved := &model.Template{Name: "j", Engine: "jinja2", Title: "{{ status | upper }}", Body: "{{ alerts | length }} alerts"}
	if err := f.templates.Save(t.Context(), saved); err != nil {
		t.Fatal(err)
	}
	cs := connect(t, f)

	var out previewOutput
	msg := call(t, cs, "preview_template", map[string]any{
		"alert_id": alertID,
		"engine":   "grafana",
		"title":    "{{ .CommonLabels.alertname }} is {{ .Status }}",
		"body":     "{{ len .Alerts.Firing }} firing",
	}, &out)
	if msg != "" {
		t.Fatal(msg)
	}
	if out.Title != "HighCPU is firing" || !strings.HasSuffix(out.Body, "firing") {
		t.Errorf("draft preview: %+v", out)
	}

	call(t, cs, "preview_template", map[string]any{"alert_id": alertID, "template_id": saved.ID}, &out)
	if out.Title != "FIRING" {
		t.Errorf("saved preview: %+v", out)
	}

	msg = call(t, cs, "preview_template", map[string]any{
		"alert_id": alertID, "engine": "grafana", "title": "{{ .Nope", "body": "",
	}, nil)
	if !strings.Contains(msg, "title") {
		t.Errorf("render error: want title error, got %q", msg)
	}
}

func TestSaveTemplate(t *testing.T) {
	f := newRepos(t)
	cs := connect(t, f)

	var created model.Template
	msg := call(t, cs, "save_template", map[string]any{
		"name": "passthrough", "engine": "jinja2", "title": "{{ title }}", "body": "{{ message }}",
	}, &created)
	if msg != "" || created.ID == "" {
		t.Fatalf("create: %q %+v", msg, created)
	}

	var updated model.Template
	call(t, cs, "save_template", map[string]any{
		"id": created.ID, "name": "passthrough", "engine": "jinja2", "title": "{{ title }}!", "body": "b",
	}, &updated)
	stored, err := f.templates.List(t.Context())
	if err != nil || len(stored) != 1 || stored[0].Title != "{{ title }}!" || updated.ID != created.ID {
		t.Fatalf("update in place: %+v, %v", stored, err)
	}

	for want, args := range map[string]map[string]any{
		"name is required":          {"name": "", "engine": "jinja2", "title": "t", "body": "b"},
		"unknown template engine":   {"name": "x", "engine": "nope", "title": "t", "body": "b"},
		"title":                     {"name": "x", "engine": "grafana", "title": "{{ .Broken", "body": "b"},
		"no template with id \"x\"": {"id": "x", "name": "x", "engine": "jinja2", "title": "t", "body": "b"},
	} {
		if msg := call(t, cs, "save_template", args, nil); !strings.Contains(msg, want) {
			t.Errorf("want error containing %q, got %q", want, msg)
		}
	}
	if stored, _ := f.templates.List(t.Context()); len(stored) != 1 {
		t.Errorf("invalid templates were saved: %d stored", len(stored))
	}
}

func TestDeleteTemplate(t *testing.T) {
	f := newRepos(t)
	used := &model.Template{Name: "used", Engine: "jinja2"}
	unused := &model.Template{Name: "unused", Engine: "jinja2"}
	for _, tm := range []*model.Template{used, unused} {
		if err := f.templates.Save(t.Context(), tm); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.rules.Save(t.Context(), &model.Rule{Name: "default", TemplateID: used.ID}); err != nil {
		t.Fatal(err)
	}
	cs := connect(t, f)

	if msg := call(t, cs, "delete_template", map[string]any{"id": used.ID}, nil); !strings.Contains(msg, `"default"`) {
		t.Errorf("deleting a template in use should name the rule, got %q", msg)
	}
	var out deleteTemplateOutput
	if msg := call(t, cs, "delete_template", map[string]any{"id": unused.ID}, &out); msg != "" || out.Deleted != unused.ID {
		t.Fatalf("delete: %q %+v", msg, out)
	}
	stored, _ := f.templates.List(t.Context())
	if len(stored) != 1 || stored[0].ID != used.ID {
		t.Errorf("remaining templates: %+v", stored)
	}
}
