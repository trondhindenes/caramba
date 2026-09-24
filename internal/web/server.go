// Package web serves caramba's webhook endpoint and server-rendered GUI.
package web

import (
	"bytes"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/trondhindenes/caramba/internal/dispatch"
	"github.com/trondhindenes/caramba/internal/engine"
	"github.com/trondhindenes/caramba/internal/format"
	"github.com/trondhindenes/caramba/internal/model"
	"github.com/trondhindenes/caramba/internal/repo"
	"github.com/trondhindenes/caramba/internal/store"
)

//go:embed templates static
var assets embed.FS

const maxPayloadBytes = 1 << 20 // 1 MiB

// Deps are the collaborators the web layer needs.
type Deps struct {
	WebhookToken string
	Alerts       *repo.AlertRepo
	Templates    *repo.TemplateRepo
	Rules        *repo.RuleRepo
	Dispatches   *repo.DispatchRepo
	Dispatcher   *dispatch.Dispatcher
	// Destinations lists the configured destination names rules can use.
	Destinations []string
	Logger       *slog.Logger
}

type Server struct {
	token          string
	alerts         *repo.AlertRepo
	templates      *repo.TemplateRepo
	rules          *repo.RuleRepo
	dispatches     *repo.DispatchRepo
	dispatcher     *dispatch.Dispatcher
	destinations   []string
	engines        engine.Registry
	logger         *slog.Logger
	listTmpl       *template.Template
	detailTmpl     *template.Template
	tmplListTmpl   *template.Template
	tmplEditTmpl   *template.Template
	ruleListTmpl   *template.Template
	ruleEditTmpl   *template.Template
	previewTmpl    *template.Template
	ruleResultTmpl *template.Template
}

func NewHandler(d Deps) http.Handler {
	s := &Server{
		token:          d.WebhookToken,
		alerts:         d.Alerts,
		templates:      d.Templates,
		rules:          d.Rules,
		dispatches:     d.Dispatches,
		dispatcher:     d.Dispatcher,
		destinations:   d.Destinations,
		engines:        engine.NewRegistry(),
		logger:         d.Logger,
		listTmpl:       parsePage("alerts_list.html"),
		detailTmpl:     parsePage("alert_detail.html", "dispatch.html"),
		tmplListTmpl:   parsePage("templates_list.html"),
		tmplEditTmpl:   parsePage("template_edit.html"),
		ruleListTmpl:   parsePage("rules_list.html"),
		ruleEditTmpl:   parsePage("rule_edit.html"),
		previewTmpl:    parseFragment("preview.html"),
		ruleResultTmpl: parseFragment("rule_test_result.html", "dispatch.html"),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("POST /webhook", s.handleWebhook)
	mux.HandleFunc("GET /{$}", s.handleAlertList)
	mux.HandleFunc("GET /alerts/{id}", s.handleAlertDetail)
	mux.HandleFunc("GET /templates", s.handleTemplateList)
	mux.HandleFunc("GET /templates/new", s.handleTemplateNew)
	mux.HandleFunc("POST /templates", s.handleTemplateCreate)
	mux.HandleFunc("GET /templates/{id}", s.handleTemplateEdit)
	mux.HandleFunc("POST /templates/{id}", s.handleTemplateUpdate)
	mux.HandleFunc("POST /templates/{id}/delete", s.handleTemplateDelete)
	mux.HandleFunc("POST /preview", s.handlePreview)
	mux.HandleFunc("GET /rules", s.handleRuleList)
	mux.HandleFunc("GET /rules/new", s.handleRuleNew)
	mux.HandleFunc("POST /rules", s.handleRuleCreate)
	mux.HandleFunc("GET /rules/{id}", s.handleRuleEdit)
	mux.HandleFunc("POST /rules/{id}", s.handleRuleUpdate)
	mux.HandleFunc("POST /rules/{id}/delete", s.handleRuleDelete)
	mux.HandleFunc("POST /rules/{id}/move", s.handleRuleMove)
	mux.HandleFunc("POST /rules/{id}/test", s.handleRuleTest)
	mux.Handle("GET /static/", http.FileServerFS(assets))
	return mux
}

var tmplFuncs = template.FuncMap{
	"add":      func(a, b int) int { return a + b },
	"contains": func(list []string, s string) bool { return slices.Contains(list, s) },
	"formatTime": func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format("2006-01-02 15:04:05") + " UTC"
	},
}

// parsePage parses a full page: the layout plus the page and any partials
// it uses.
func parsePage(files ...string) *template.Template {
	paths := []string{"templates/layout.html"}
	for _, f := range files {
		paths = append(paths, "templates/"+f)
	}
	return template.Must(template.New("layout.html").Funcs(tmplFuncs).ParseFS(assets, paths...))
}

// parseFragment parses an htmx fragment rendered without the layout.
func parseFragment(files ...string) *template.Template {
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = "templates/" + f
	}
	return template.Must(template.New(files[0]).Funcs(tmplFuncs).ParseFS(assets, paths...))
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPayloadBytes))
	if err != nil {
		http.Error(w, "payload too large or unreadable", http.StatusBadRequest)
		return
	}
	stored, err := s.alerts.Save(r.Context(), body, time.Now())
	if err != nil {
		if errors.Is(err, repo.ErrInvalidPayload) {
			http.Error(w, "invalid alert payload", http.StatusBadRequest)
			return
		}
		s.logger.Error("persisting alert failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.logger.Info("alert received",
		"id", stored.ID,
		"status", stored.Payload.Status,
		"receiver", stored.Payload.Receiver,
		"alerts", len(stored.Payload.Alerts))
	s.dispatcher.Go(stored)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "id": stored.ID})
}

func (s *Server) authorized(r *http.Request) bool {
	want := "Bearer " + s.token
	got := r.Header.Get("Authorization")
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func (s *Server) handleAlertList(w http.ResponseWriter, r *http.Request) {
	alerts, truncated, err := s.alerts.List(r.Context())
	if err != nil {
		s.serverError(w, "listing alerts", err)
		return
	}
	statusFilter := r.URL.Query().Get("status")
	if statusFilter != "" {
		filtered := alerts[:0]
		for _, a := range alerts {
			if a.Payload.Status == statusFilter {
				filtered = append(filtered, a)
			}
		}
		alerts = filtered
	}
	s.render(w, s.listTmpl, map[string]any{
		"Alerts":    alerts,
		"Status":    statusFilter,
		"Truncated": truncated,
	})
}

func (s *Server) handleAlertDetail(w http.ResponseWriter, r *http.Request) {
	alert, err := s.alerts.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		s.serverError(w, "loading alert", err)
		return
	}
	templates, err := s.templates.List(r.Context())
	if err != nil {
		s.serverError(w, "listing templates", err)
		return
	}
	dispatched, err := s.dispatches.Get(r.Context(), alert.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.serverError(w, "loading dispatch", err)
		return
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, alert.Raw, "", "  "); err != nil {
		pretty.Write(alert.Raw)
	}
	s.render(w, s.detailTmpl, map[string]any{
		"Alert":     alert,
		"Dispatch":  dispatched,
		"Templates": templates,
		"Formats":   format.Names(),
		"PrettyRaw": pretty.String(),
	})
}

func (s *Server) handleTemplateList(w http.ResponseWriter, r *http.Request) {
	templates, err := s.templates.List(r.Context())
	if err != nil {
		s.serverError(w, "listing templates", err)
		return
	}
	s.render(w, s.tmplListTmpl, map[string]any{"Templates": templates})
}

func (s *Server) handleTemplateNew(w http.ResponseWriter, r *http.Request) {
	s.renderTemplateEditor(w, r, &model.Template{Engine: "grafana"}, "")
}

func (s *Server) handleTemplateEdit(w http.ResponseWriter, r *http.Request) {
	tmpl, err := s.templates.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		s.serverError(w, "loading template", err)
		return
	}
	s.renderTemplateEditor(w, r, tmpl, "")
}

func (s *Server) handleTemplateCreate(w http.ResponseWriter, r *http.Request) {
	s.saveTemplate(w, r, "")
}

func (s *Server) handleTemplateUpdate(w http.ResponseWriter, r *http.Request) {
	s.saveTemplate(w, r, r.PathValue("id"))
}

func (s *Server) saveTemplate(w http.ResponseWriter, r *http.Request, id string) {
	tmpl := &model.Template{
		ID:     id,
		Name:   r.FormValue("name"),
		Engine: r.FormValue("engine"),
		Title:  r.FormValue("title"),
		Body:   r.FormValue("body"),
	}
	if tmpl.Name == "" {
		s.renderTemplateEditor(w, r, tmpl, "name is required")
		return
	}
	if err := s.engines.ValidateTemplate(tmpl); err != nil {
		s.renderTemplateEditor(w, r, tmpl, err.Error())
		return
	}
	if err := s.templates.Save(r.Context(), tmpl); err != nil {
		s.serverError(w, "saving template", err)
		return
	}
	http.Redirect(w, r, "/templates", http.StatusSeeOther)
}

func (s *Server) handleTemplateDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.templates.Delete(r.Context(), r.PathValue("id")); err != nil {
		s.serverError(w, "deleting template", err)
		return
	}
	http.Redirect(w, r, "/templates", http.StatusSeeOther)
}

func (s *Server) renderTemplateEditor(w http.ResponseWriter, r *http.Request, tmpl *model.Template, errMsg string) {
	alerts, _, err := s.alerts.List(r.Context())
	if err != nil {
		s.serverError(w, "listing alerts", err)
		return
	}
	s.render(w, s.tmplEditTmpl, map[string]any{
		"Template": tmpl,
		"Engines":  s.engines.Names(),
		"Formats":  format.Names(),
		"Alerts":   alerts,
		"Error":    errMsg,
	})
}

// handlePreview renders a template against a stored alert and returns an
// HTML fragment for the preview pane. It accepts either a saved template
// (template_id) or unsaved editor contents (engine/title/body), so the
// editor can preview without saving first. The format field picks how the
// body is displayed: raw text, or as Slack or Markdown would show it.
func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	bodyFormat := r.FormValue("format")
	result := func(rendered *engine.Rendered, err error) {
		data := map[string]any{}
		var body template.HTML
		if err == nil {
			body, err = format.Render(bodyFormat, rendered.Body)
		}
		if err != nil {
			data["Error"] = err.Error()
		} else {
			data["Title"] = rendered.Title
			data["Body"] = body
			data["Formatted"] = bodyFormat != "" && bodyFormat != format.Raw
		}
		var buf bytes.Buffer
		if err := s.previewTmpl.ExecuteTemplate(&buf, "preview.html", data); err != nil {
			s.serverError(w, "rendering preview", err)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		buf.WriteTo(w)
	}

	alertID := r.FormValue("alert_id")
	if alertID == "" {
		result(nil, errors.New("choose an alert to preview against"))
		return
	}
	alert, err := s.alerts.Get(r.Context(), alertID)
	if err != nil {
		result(nil, fmt.Errorf("loading alert: %w", err))
		return
	}

	tmpl := &model.Template{
		Engine: r.FormValue("engine"),
		Title:  r.FormValue("title"),
		Body:   r.FormValue("body"),
	}
	if id := r.FormValue("template_id"); id != "" {
		tmpl, err = s.templates.Get(r.Context(), id)
		if err != nil {
			result(nil, fmt.Errorf("loading template: %w", err))
			return
		}
	}
	result(s.engines.RenderTemplate(tmpl, alert.Payload))
}

// render executes into a buffer first so template errors become a clean 500
// instead of a half-written page.
func (s *Server) render(w http.ResponseWriter, tmpl *template.Template, data any) {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout.html", data); err != nil {
		s.serverError(w, "rendering page", err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}

func (s *Server) serverError(w http.ResponseWriter, what string, err error) {
	s.logger.Error(what+" failed", "error", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}
