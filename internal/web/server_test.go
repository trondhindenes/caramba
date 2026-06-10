package web

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/trondhindenes/caramba/internal/config"
	"github.com/trondhindenes/caramba/internal/repo"
	"github.com/trondhindenes/caramba/internal/store/localdir"
)

const testToken = "test-token"

func newTestServer(t *testing.T) (*httptest.Server, *repo.AlertRepo) {
	t.Helper()
	st, err := localdir.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	alerts := repo.NewAlertRepo(st)
	cfg := &config.Config{WebhookToken: testToken}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ts := httptest.NewServer(NewHandler(cfg, alerts, logger))
	t.Cleanup(ts.Close)
	return ts, alerts
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
	ts, _ := newTestServer(t)
	if resp := postWebhook(t, ts, "", fixture(t)); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: want 401, got %d", resp.StatusCode)
	}
	if resp := postWebhook(t, ts, "wrong", fixture(t)); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong token: want 401, got %d", resp.StatusCode)
	}
}

func TestWebhookIngest(t *testing.T) {
	ts, alerts := newTestServer(t)
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
	ts, _ := newTestServer(t)
	if resp := postWebhook(t, ts, testToken, []byte("not json")); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

func TestAlertListPage(t *testing.T) {
	ts, alerts := newTestServer(t)
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
	ts, alerts := newTestServer(t)
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
	ts, alerts := newTestServer(t)
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
	ts, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/alerts/2026-06-10T00:00:00.000Z-NOPE")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}
