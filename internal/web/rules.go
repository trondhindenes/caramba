package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/trondhindenes/caramba/internal/model"
	"github.com/trondhindenes/caramba/internal/route"
	"github.com/trondhindenes/caramba/internal/store"
)

// ruleRow is a rule as the rules list shows it.
type ruleRow struct {
	*model.Rule
	TemplateName string
	// Unreachable marks rules below a catch-all, which can never match.
	Unreachable bool
}

func (s *Server) handleRuleList(w http.ResponseWriter, r *http.Request) {
	rules, err := s.rules.List(r.Context())
	if err != nil {
		s.serverError(w, "listing rules", err)
		return
	}
	names, err := s.templateNames(r)
	if err != nil {
		s.serverError(w, "listing templates", err)
		return
	}
	rows := make([]ruleRow, len(rules))
	catchAll := false
	for i, rule := range rules {
		rows[i] = ruleRow{Rule: rule, TemplateName: names[rule.TemplateID], Unreachable: catchAll}
		catchAll = catchAll || len(rule.Matchers) == 0
	}
	s.render(w, s.ruleListTmpl, map[string]any{
		"Rules":        rows,
		"Destinations": s.destinations,
	})
}

func (s *Server) templateNames(r *http.Request) (map[string]string, error) {
	templates, err := s.templates.List(r.Context())
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, t := range templates {
		names[t.ID] = t.Name
	}
	return names, nil
}

func (s *Server) handleRuleNew(w http.ResponseWriter, r *http.Request) {
	s.renderRuleEditor(w, r, &model.Rule{}, "", "")
}

func (s *Server) handleRuleEdit(w http.ResponseWriter, r *http.Request) {
	rule, err := s.rules.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.ruleLoadError(w, r, err)
		return
	}
	s.renderRuleEditor(w, r, rule, route.FormatMatchers(rule.Matchers), "")
}

func (s *Server) handleRuleCreate(w http.ResponseWriter, r *http.Request) {
	s.saveRule(w, r, "")
}

func (s *Server) handleRuleUpdate(w http.ResponseWriter, r *http.Request) {
	s.saveRule(w, r, r.PathValue("id"))
}

func (s *Server) saveRule(w http.ResponseWriter, r *http.Request, id string) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	matchersText := r.FormValue("matchers")
	rule := &model.Rule{
		ID:           id,
		Name:         r.FormValue("name"),
		TemplateID:   r.FormValue("template_id"),
		Destinations: r.Form["destinations"],
	}
	fail := func(msg string) { s.renderRuleEditor(w, r, rule, matchersText, msg) }

	matchers, err := route.ParseMatchers(matchersText)
	if err != nil {
		fail(err.Error())
		return
	}
	rule.Matchers = matchers
	if rule.Name == "" {
		fail("name is required")
		return
	}
	if _, err := s.templates.Get(r.Context(), rule.TemplateID); err != nil {
		fail("choose a template")
		return
	}
	for _, d := range rule.Destinations {
		if !slices.Contains(s.destinations, d) {
			fail(fmt.Sprintf("unknown destination %q", d))
			return
		}
	}
	if err := s.rules.Save(r.Context(), rule); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		s.serverError(w, "saving rule", err)
		return
	}
	http.Redirect(w, r, "/rules", http.StatusSeeOther)
}

func (s *Server) handleRuleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.rules.Delete(r.Context(), r.PathValue("id")); err != nil {
		s.serverError(w, "deleting rule", err)
		return
	}
	http.Redirect(w, r, "/rules", http.StatusSeeOther)
}

func (s *Server) handleRuleMove(w http.ResponseWriter, r *http.Request) {
	delta := 1
	if r.FormValue("direction") == "up" {
		delta = -1
	}
	if err := s.rules.Move(r.Context(), r.PathValue("id"), delta); err != nil {
		s.ruleLoadError(w, r, err)
		return
	}
	http.Redirect(w, r, "/rules", http.StatusSeeOther)
}

// handleRuleTest sends a stored alert through a saved rule for real,
// regardless of whether the rule's matchers match it, and returns an HTML
// fragment with the outcome.
func (s *Server) handleRuleTest(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{}
	defer func() {
		var buf bytes.Buffer
		if err := s.ruleResultTmpl.ExecuteTemplate(&buf, "rule_test_result.html", data); err != nil {
			s.serverError(w, "rendering test result", err)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		buf.WriteTo(w)
	}()

	rule, err := s.rules.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		data["Error"] = "loading rule: " + err.Error()
		return
	}
	alert, err := s.alerts.Get(r.Context(), r.FormValue("alert_id"))
	if err != nil {
		data["Error"] = "choose an alert to send"
		return
	}
	var doc any
	if err := json.Unmarshal(alert.Raw, &doc); err == nil {
		data["Matches"] = route.Matches(rule, doc)
	}
	data["Dispatch"] = s.dispatcher.Deliver(r.Context(), rule, alert)
}

func (s *Server) ruleLoadError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	s.serverError(w, "loading rule", err)
}

func (s *Server) renderRuleEditor(w http.ResponseWriter, r *http.Request, rule *model.Rule, matchersText, errMsg string) {
	templates, err := s.templates.List(r.Context())
	if err != nil {
		s.serverError(w, "listing templates", err)
		return
	}
	alerts, _, err := s.alerts.List(r.Context())
	if err != nil {
		s.serverError(w, "listing alerts", err)
		return
	}
	s.render(w, s.ruleEditTmpl, map[string]any{
		"Rule":         rule,
		"MatchersText": matchersText,
		"Templates":    templates,
		"Destinations": s.destinations,
		"Alerts":       alerts,
		"Error":        errMsg,
	})
}
