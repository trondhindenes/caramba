// Package gotmpl renders Grafana-style notification templates using Go's
// text/template. The dot context and function set mirror Grafana's, so
// templates authored here can be ported back into Grafana notification
// templates.
package gotmpl

import (
	"regexp"
	"strings"
	"text/template"
	"unicode"

	"github.com/trondhindenes/caramba/internal/model"
)

const Name = "grafana"

type Engine struct{}

func New() *Engine { return &Engine{} }

func (e *Engine) Name() string { return Name }

// alerts mirrors Grafana's ExtendedAlerts: the .Alerts value supports both
// `range .Alerts` and `.Alerts.Firing` / `.Alerts.Resolved`.
type alerts []model.Alert

func (as alerts) Firing() []model.Alert   { return as.withStatus("firing") }
func (as alerts) Resolved() []model.Alert { return as.withStatus("resolved") }

func (as alerts) withStatus(status string) []model.Alert {
	var out []model.Alert
	for _, a := range as {
		if a.Status == status {
			out = append(out, a)
		}
	}
	return out
}

// data mirrors Grafana's ExtendedData, the dot context of notification
// templates.
type data struct {
	Receiver          string
	Status            string
	Alerts            alerts
	GroupLabels       map[string]string
	CommonLabels      map[string]string
	CommonAnnotations map[string]string
	ExternalURL       string
}

// funcs is the subset of Grafana's notification template functions caramba
// supports. Keep the template-edit page help text in sync when extending.
var funcs = template.FuncMap{
	"toUpper":   strings.ToUpper,
	"toLower":   strings.ToLower,
	"trimSpace": strings.TrimSpace,
	"title":     titleCase,
	"join":      func(sep string, s []string) string { return strings.Join(s, sep) },
	"match":     regexp.MatchString,
	"reReplaceAll": func(pattern, replacement, text string) (string, error) {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return "", err
		}
		return re.ReplaceAllString(text, replacement), nil
	},
}

func (e *Engine) Render(src string, p *model.Payload) (string, error) {
	tmpl, err := parse(src)
	if err != nil {
		return "", err
	}
	var buf strings.Builder
	err = tmpl.Execute(&buf, &data{
		Receiver:          p.Receiver,
		Status:            p.Status,
		Alerts:            alerts(p.Alerts),
		GroupLabels:       p.GroupLabels,
		CommonLabels:      p.CommonLabels,
		CommonAnnotations: p.CommonAnnotations,
		ExternalURL:       p.ExternalURL,
	})
	return buf.String(), err
}

func (e *Engine) Validate(src string) error {
	_, err := parse(src)
	return err
}

func parse(src string) (*template.Template, error) {
	return template.New("template").Funcs(funcs).Parse(src)
}

func titleCase(s string) string {
	prev := ' '
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(prev) {
			prev = r
			return unicode.ToUpper(r)
		}
		prev = r
		return r
	}, s)
}
