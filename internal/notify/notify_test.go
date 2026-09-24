package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/trondhindenes/caramba/internal/config"
	"github.com/trondhindenes/caramba/internal/engine"
)

func slackServer(t *testing.T, statuses ...int) (*httptest.Server, *atomic.Int32, *atomic.Value) {
	t.Helper()
	var calls atomic.Int32
	var lastText atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1))
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		lastText.Store(body["text"])
		status := statuses[min(n, len(statuses))-1]
		w.WriteHeader(status)
		if status != http.StatusOK {
			w.Write([]byte("oops"))
		}
	}))
	t.Cleanup(ts.Close)
	return ts, &calls, &lastText
}

func registry(url string) *Registry {
	r := NewRegistry([]config.Destination{{Name: "ops", Type: "slack", WebhookURL: url}})
	r.Backoff = []time.Duration{time.Millisecond, time.Millisecond}
	return r
}

var msg = &engine.Rendered{Title: "🔴 api down", Body: "*api* in `prod`\n"}

func TestSlackSendsTitleAndBody(t *testing.T) {
	ts, calls, text := slackServer(t, 200)
	attempts, err := registry(ts.URL).Send(context.Background(), "ops", msg)
	if err != nil || attempts != 1 || calls.Load() != 1 {
		t.Fatalf("attempts=%d calls=%d err=%v", attempts, calls.Load(), err)
	}
	if text.Load() != "*🔴 api down*\n*api* in `prod`" {
		t.Errorf("text: %q", *text)
	}
}

func TestRetriesTransientFailures(t *testing.T) {
	ts, calls, _ := slackServer(t, 500, 429, 200)
	attempts, err := registry(ts.URL).Send(context.Background(), "ops", msg)
	if err != nil || attempts != 3 || calls.Load() != 3 {
		t.Fatalf("attempts=%d calls=%d err=%v", attempts, calls.Load(), err)
	}
}

func TestGivesUpAfterBackoff(t *testing.T) {
	ts, calls, _ := slackServer(t, 503)
	attempts, err := registry(ts.URL).Send(context.Background(), "ops", msg)
	if err == nil || attempts != 3 || calls.Load() != 3 {
		t.Fatalf("attempts=%d calls=%d err=%v", attempts, calls.Load(), err)
	}
}

func TestNoRetryOnClientError(t *testing.T) {
	ts, calls, _ := slackServer(t, 404)
	attempts, err := registry(ts.URL).Send(context.Background(), "ops", msg)
	if err == nil || !strings.Contains(err.Error(), "404") || attempts != 1 || calls.Load() != 1 {
		t.Fatalf("attempts=%d calls=%d err=%v", attempts, calls.Load(), err)
	}
}

func TestErrorsHideWebhookURL(t *testing.T) {
	secret := "http://127.0.0.1:1/services/SECRET"
	_, err := registry(secret).Send(context.Background(), "ops", msg)
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("error must not leak the URL: %v", err)
	}
}

func TestUnknownDestination(t *testing.T) {
	_, err := registry("http://x").Send(context.Background(), "nope", msg)
	if err == nil || !strings.Contains(err.Error(), "unknown destination") {
		t.Fatalf("got %v", err)
	}
}
