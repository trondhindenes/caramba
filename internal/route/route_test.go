package route

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/trondhindenes/caramba/internal/model"
)

func payload(t *testing.T) any {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/grafana-payload.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestMatches(t *testing.T) {
	doc := payload(t)
	cases := []struct {
		name  string
		field string
		pat   string
		want  bool
	}{
		{"title wildcard", "title", "*highcpu*", true},
		{"title miss", "title", "*container restarts*", false},
		{"exact label", "commonLabels.alertname", "HighCPU", true},
		{"label is case-insensitive", "commonLabels.severity", "CRITICAL", true},
		{"label wildcard", "commonLabels.alertname", "High*", true},
		{"anchored", "commonLabels.alertname", "High", false},
		{"question mark", "commonLabels.alertname", "HighCP?", true},
		{"any alert in group", "alerts.labels.instance", "web-2.*", true},
		{"no alert matches", "alerts.labels.instance", "db-*", false},
		{"number value", "orgId", "1", true},
		{"missing path", "commonLabels.nope", "*", false},
		{"object is not a value", "commonLabels", "*", false},
		{"regex chars are literal", "commonLabels.alertname", "High.PU", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &model.Rule{Matchers: []model.Matcher{{Field: tc.field, Pattern: tc.pat}}}
			if got := Matches(r, doc); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFirstMatch(t *testing.T) {
	raw, _ := os.ReadFile("../../testdata/grafana-payload.json")
	restarts := &model.Rule{Name: "restarts", Matchers: []model.Matcher{{Field: "title", Pattern: "*container restarts*"}}}
	both := &model.Rule{Name: "cpu critical", Matchers: []model.Matcher{
		{Field: "commonLabels.alertname", Pattern: "HighCPU"},
		{Field: "commonLabels.severity", Pattern: "critical"},
	}}
	fallback := &model.Rule{Name: "default"}

	got, err := FirstMatch([]*model.Rule{restarts, both, fallback}, raw)
	if err != nil || got != both {
		t.Fatalf("want cpu critical, got %v (%v)", got, err)
	}
	got, _ = FirstMatch([]*model.Rule{restarts, fallback, both}, raw)
	if got != fallback {
		t.Errorf("first match should win: got %v", got)
	}
	got, _ = FirstMatch([]*model.Rule{restarts}, raw)
	if got != nil {
		t.Errorf("want no match, got %v", got)
	}
}

func TestParseMatchers(t *testing.T) {
	ms, err := ParseMatchers("title = *container restarts*\n\n# comment\n commonLabels.namespace=sartor-*  \n")
	if err != nil {
		t.Fatal(err)
	}
	want := []model.Matcher{{Field: "title", Pattern: "*container restarts*"}, {Field: "commonLabels.namespace", Pattern: "sartor-*"}}
	if len(ms) != 2 || ms[0] != want[0] || ms[1] != want[1] {
		t.Errorf("got %+v", ms)
	}
	if FormatMatchers(ms) != "title = *container restarts*\ncommonLabels.namespace = sartor-*" {
		t.Errorf("format: %q", FormatMatchers(ms))
	}
	for _, bad := range []string{"no equals", "= x", "title =", "a..b = x", ".a = x"} {
		if _, err := ParseMatchers(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}
