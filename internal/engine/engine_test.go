package engine

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/trondhindenes/caramba/internal/model"
)

func fixture(t *testing.T) *model.Payload {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/grafana-payload.json")
	if err != nil {
		t.Fatal(err)
	}
	var p model.Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return &p
}

func TestGrafanaEngine(t *testing.T) {
	p := fixture(t)
	reg := NewRegistry()

	cases := []struct {
		name, src, want string
	}{
		{"status", `{{ .Status }}`, "firing"},
		{"status func", `{{ .Status | toUpper }}`, "FIRING"},
		{"group label", `{{ .GroupLabels.alertname }}`, "HighCPU"},
		{"common annotation", `{{ .CommonAnnotations.summary }}`, "CPU usage above 90% for 5 minutes"},
		{"range alerts", `{{ range .Alerts }}{{ .Labels.instance }} {{ end }}`, "web-1.example.com web-2.example.com "},
		{"firing filter", `{{ len .Alerts.Firing }}`, "2"},
		{"resolved filter", `{{ len .Alerts.Resolved }}`, "0"},
		{"reReplaceAll", `{{ reReplaceAll "\\.example\\.com" "" "web-1.example.com" }}`, "web-1"},
	}
	e, err := reg.Get("grafana")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := e.Render(tc.src, p)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestJinjaEngine(t *testing.T) {
	p := fixture(t)
	reg := NewRegistry()

	cases := []struct {
		name, src, want string
	}{
		{"status", `{{ status }}`, "firing"},
		{"filter", `{{ status | upper }}`, "FIRING"},
		{"group label", `{{ groupLabels.alertname }}`, "HighCPU"},
		{"loop", `{% for a in alerts %}{{ a.labels.instance }} {% endfor %}`, "web-1.example.com web-2.example.com "},
		{"condition", `{% if status == "firing" %}FIRE{% endif %}`, "FIRE"},
		{"length", `{{ alerts | length }}`, "2"},
	}
	e, err := reg.Get("jinja2")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := e.Render(tc.src, p)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderTemplateTitleAndBody(t *testing.T) {
	p := fixture(t)
	reg := NewRegistry()

	rendered, err := reg.RenderTemplate(&model.Template{
		Engine: "grafana",
		Title:  `[{{ .Status | toUpper }}] {{ .GroupLabels.alertname }}`,
		Body:   `{{ .CommonAnnotations.summary }}`,
	}, p)
	if err != nil {
		t.Fatal(err)
	}
	if rendered.Title != "[FIRING] HighCPU" {
		t.Errorf("title: %q", rendered.Title)
	}
	if rendered.Body != "CPU usage above 90% for 5 minutes" {
		t.Errorf("body: %q", rendered.Body)
	}
}

func TestRenderErrors(t *testing.T) {
	p := fixture(t)
	reg := NewRegistry()

	if _, err := reg.RenderTemplate(&model.Template{Engine: "nope"}, p); err == nil {
		t.Error("unknown engine should error")
	}
	if _, err := reg.RenderTemplate(&model.Template{Engine: "grafana", Title: `{{ .Status`}, p); err == nil {
		t.Error("unparsable grafana title should error")
	}
	if _, err := reg.RenderTemplate(&model.Template{Engine: "jinja2", Body: `{% if %}`}, p); err == nil {
		t.Error("unparsable jinja body should error")
	}
}

func TestValidateTemplate(t *testing.T) {
	reg := NewRegistry()
	err := reg.ValidateTemplate(&model.Template{Engine: "grafana", Title: "ok", Body: `{{ bad`})
	if err == nil || !strings.Contains(err.Error(), "body") {
		t.Errorf("want body parse error, got %v", err)
	}
	if err := reg.ValidateTemplate(&model.Template{Engine: "jinja2", Title: `{{ status }}`, Body: "ok"}); err != nil {
		t.Errorf("valid template should pass: %v", err)
	}
}
