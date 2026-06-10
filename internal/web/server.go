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
	"time"

	"github.com/trondhindenes/caramba/internal/config"
	"github.com/trondhindenes/caramba/internal/repo"
	"github.com/trondhindenes/caramba/internal/store"
)

//go:embed templates static
var assets embed.FS

const maxPayloadBytes = 1 << 20 // 1 MiB

type Server struct {
	token      string
	alerts     *repo.AlertRepo
	logger     *slog.Logger
	listTmpl   *template.Template
	detailTmpl *template.Template
}

func NewHandler(cfg *config.Config, alerts *repo.AlertRepo, logger *slog.Logger) http.Handler {
	s := &Server{
		token:      cfg.WebhookToken,
		alerts:     alerts,
		logger:     logger,
		listTmpl:   parsePage("alerts_list.html"),
		detailTmpl: parsePage("alert_detail.html"),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("POST /webhook", s.handleWebhook)
	mux.HandleFunc("GET /{$}", s.handleAlertList)
	mux.HandleFunc("GET /alerts/{id}", s.handleAlertDetail)
	mux.Handle("GET /static/", http.FileServerFS(assets))
	return mux
}

var tmplFuncs = template.FuncMap{
	"formatTime": func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format("2006-01-02 15:04:05") + " UTC"
	},
}

func parsePage(page string) *template.Template {
	return template.Must(template.New("layout.html").Funcs(tmplFuncs).
		ParseFS(assets, "templates/layout.html", "templates/"+page))
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
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, alert.Raw, "", "  "); err != nil {
		pretty.Write(alert.Raw)
	}
	s.render(w, s.detailTmpl, map[string]any{
		"Alert":     alert,
		"PrettyRaw": pretty.String(),
	})
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
