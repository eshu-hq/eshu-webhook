// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// observed bundles a server whose log output is captured and whose stub
// core records submissions, so a case can assert the log line and the
// /metrics series a single request produced.
type observed struct {
	t    *testing.T
	srv  *Server
	core *stubCore
	logs *bytes.Buffer
}

func newObserved(t *testing.T, mutate func(*Config)) *observed {
	t.Helper()
	cfg := fullTestConfig()
	if mutate != nil {
		mutate(&cfg)
	}
	srv, stub := serveMux(t, cfg)
	logs := &bytes.Buffer{}
	srv.WithLogger(slog.New(slog.NewJSONHandler(logs, nil)))
	return &observed{t: t, srv: srv, core: stub, logs: logs}
}

func (o *observed) do(req *http.Request) *httptest.ResponseRecorder {
	o.t.Helper()
	rec := httptest.NewRecorder()
	o.srv.Mux().ServeHTTP(rec, req)
	return rec
}

// logLine returns the single request log record, failing when the request
// produced zero or several.
func (o *observed) logLine() map[string]any {
	o.t.Helper()
	lines := strings.Split(strings.TrimSpace(o.logs.String()), "\n")
	if len(lines) != 1 || lines[0] == "" {
		o.t.Fatalf("log lines = %d, want exactly 1:\n%s", len(lines), o.logs.String())
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		o.t.Fatalf("log line is not JSON: %v\n%s", err, lines[0])
	}
	return record
}

// scrape returns the /metrics exposition text.
func (o *observed) scrape() string {
	o.t.Helper()
	rec := o.do(httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		o.t.Fatalf("/metrics status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain; version=0.0.4") {
		o.t.Fatalf("/metrics Content-Type = %q, want Prometheus text 0.0.4", got)
	}
	return rec.Body.String()
}

func (o *observed) wantSeries(series string) {
	o.t.Helper()
	if text := o.scrape(); !strings.Contains(text, series+"\n") {
		o.t.Fatalf("/metrics missing series line %q in:\n%s", series, text)
	}
}

func (o *observed) wantLog(want map[string]any) {
	o.t.Helper()
	line := o.logLine()
	for key, value := range want {
		if got := line[key]; got != value {
			o.t.Fatalf("log %q = %v (%T), want %v\nrecord: %v", key, got, got, value, line)
		}
	}
	if line["msg"] != "webhook request" {
		o.t.Fatalf("log msg = %v, want %q", line["msg"], "webhook request")
	}
	if _, ok := line["duration_ms"]; !ok {
		o.t.Fatalf("log record missing duration_ms: %v", line)
	}
}

func githubRequest(secret, event, delivery, payload string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(payload))
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-GitHub-Delivery", delivery)
	req.Header.Set("X-Hub-Signature-256", signSHA256(secret, []byte(payload)))
	return req
}

func TestTelemetryAcceptedDelivery(t *testing.T) {
	t.Parallel()

	o := newObserved(t, nil)
	rec := o.do(githubRequest("gh-secret", "push", "d-ok", githubPushPayload))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", rec.Code, rec.Body.String())
	}

	o.wantLog(map[string]any{
		"level":       "INFO",
		"route":       "/webhooks/github",
		"provider":    "github",
		"status":      float64(202),
		"outcome":     "accepted",
		"reason":      "accepted",
		"delivery_id": "d-ok",
	})
	o.wantSeries(`eshu_webhook_requests_total{provider="github",outcome="accepted",reason="accepted"} 1`)
	o.wantSeries(`eshu_webhook_request_duration_seconds_count{provider="github"} 1`)
}

func TestTelemetryBadSignature(t *testing.T) {
	t.Parallel()

	o := newObserved(t, nil)
	req := githubRequest("wrong-secret", "push", "d-bad", githubPushPayload)
	rec := o.do(req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	o.wantLog(map[string]any{
		"level":       "WARN",
		"provider":    "github",
		"status":      float64(401),
		"outcome":     "rejected",
		"reason":      "bad_signature",
		"delivery_id": "d-bad",
	})
	o.wantSeries(`eshu_webhook_requests_total{provider="github",outcome="rejected",reason="bad_signature"} 1`)
	if len(o.core.submitted) != 0 {
		t.Fatalf("intake calls = %d, want 0", len(o.core.submitted))
	}
}

func TestTelemetryBadTokenOnTokenProvider(t *testing.T) {
	t.Parallel()

	o := newObserved(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/aws/eventbridge", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer wrong")
	if rec := o.do(req); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	o.wantLog(map[string]any{"provider": "aws", "reason": "bad_token", "outcome": "rejected"})
	o.wantSeries(`eshu_webhook_requests_total{provider="aws",outcome="rejected",reason="bad_token"} 1`)
}

func TestTelemetryOversizedBody(t *testing.T) {
	t.Parallel()

	o := newObserved(t, nil)
	over := strings.Repeat("x", int(DefaultMaxBodyBytes)+1)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(over))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "d-big")
	if rec := o.do(req); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}

	o.wantLog(map[string]any{
		"level":    "WARN",
		"provider": "github",
		"status":   float64(413),
		"outcome":  "rejected",
		"reason":   "oversized_body",
	})
	o.wantSeries(`eshu_webhook_requests_total{provider="github",outcome="rejected",reason="oversized_body"} 1`)
}

func TestTelemetryUnknownEvent(t *testing.T) {
	t.Parallel()

	o := newObserved(t, nil)
	rec := o.do(githubRequest("gh-secret", "star", "d-star", `{"zen":"hi"}`))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}

	o.wantLog(map[string]any{
		"level":       "WARN",
		"status":      float64(422),
		"outcome":     "rejected",
		"reason":      "unsupported_event",
		"delivery_id": "d-star",
	})
	o.wantSeries(`eshu_webhook_requests_total{provider="github",outcome="rejected",reason="unsupported_event"} 1`)
}

func TestTelemetryInvalidPayload(t *testing.T) {
	t.Parallel()

	o := newObserved(t, nil)
	rec := o.do(githubRequest("gh-secret", "push", "d-junk", `{not json`))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}

	o.wantLog(map[string]any{"status": float64(422), "reason": "invalid_payload", "outcome": "rejected"})
	o.wantSeries(`eshu_webhook_requests_total{provider="github",outcome="rejected",reason="invalid_payload"} 1`)
}

func TestTelemetryIntakeFailure(t *testing.T) {
	t.Parallel()

	// Port 1 refuses connections, which reads like a core outage.
	cfg := fullTestConfig()
	cfg.IntakeURL = "http://127.0.0.1:1"
	srv, err := NewServerWithClient(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	srv.WithLogger(slog.New(slog.NewJSONHandler(logs, nil)))
	o := &observed{t: t, srv: srv, core: &stubCore{t: t}, logs: logs}

	rec := o.do(githubRequest("gh-secret", "push", "d-502", githubPushPayload))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}

	o.wantLog(map[string]any{
		"level":    "ERROR",
		"status":   float64(502),
		"outcome":  "failed",
		"reason":   "intake_failed",
		"provider": "github",
	})
	o.wantSeries(`eshu_webhook_requests_total{provider="github",outcome="failed",reason="intake_failed"} 1`)
}

func TestTelemetryDeliveryConflict(t *testing.T) {
	t.Parallel()

	o := newObserved(t, nil)
	o.core.conflict = true
	rec := o.do(githubRequest("gh-secret", "push", "d-409", githubPushPayload))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}

	o.wantLog(map[string]any{"status": float64(409), "outcome": "rejected", "reason": "delivery_conflict"})
	o.wantSeries(`eshu_webhook_requests_total{provider="github",outcome="rejected",reason="delivery_conflict"} 1`)
}

func TestTelemetryMethodNotAllowed(t *testing.T) {
	t.Parallel()

	o := newObserved(t, nil)
	rec := o.do(httptest.NewRequest(http.MethodGet, "/webhooks/gitlab", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}

	o.wantLog(map[string]any{"provider": "gitlab", "reason": "method_not_allowed", "outcome": "rejected"})
	o.wantSeries(`eshu_webhook_requests_total{provider="gitlab",outcome="rejected",reason="method_not_allowed"} 1`)
}

func TestTelemetryUnreadableBody(t *testing.T) {
	t.Parallel()

	o := newObserved(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", nil)
	req.Body = errReader{}
	if rec := o.do(req); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}

	o.wantLog(map[string]any{"status": float64(400), "reason": "unreadable_body", "outcome": "rejected"})
}

// TestTelemetryDeliveryIDFallsBackToContentHash proves a provider without a
// delivery header still logs a stable id, and that the id is derived rather
// than the body itself.
func TestTelemetryDeliveryIDFallsBackToContentHash(t *testing.T) {
	t.Parallel()

	o := newObserved(t, nil)
	payload := `{"event":{"id":"e1","event_type":"incident.triggered"},"incident":{"id":"i1"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/pagerduty", strings.NewReader(payload))
	req.Header.Set("X-PagerDuty-Signature", signV1("pd-secret", []byte(payload)))
	if rec := o.do(req); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", rec.Code, rec.Body.String())
	}

	id, _ := o.logLine()["delivery_id"].(string)
	if want := deliveryID("", []byte(payload)); id != want {
		t.Fatalf("delivery_id = %q, want %q", id, want)
	}
}

// TestTelemetryNeverLogsBodyOrSecrets sends every credential shape through
// every outcome and proves none of the secret values, signature values, or
// body content reaches a log line or the /metrics text.
func TestTelemetryNeverLogsBodyOrSecrets(t *testing.T) {
	t.Parallel()

	const sentinel = "BODY-SENTINEL-7f3a"
	o := newObserved(t, nil)
	body := `{"zen":"` + sentinel + `","ref":"refs/heads/main"}`

	signed := githubRequest("gh-secret", "star", "d-leak", body)
	o.do(signed)
	bad := githubRequest("not-the-secret", "push", "d-leak2", body)
	o.do(bad)
	token := httptest.NewRequest(http.MethodPost, "/webhook/gcp-freshness", strings.NewReader(body))
	token.Header.Set("Authorization", "Bearer wrong-token-value")
	o.do(token)
	valid := httptest.NewRequest(http.MethodPost, "/webhook/gcp-freshness", strings.NewReader(body))
	valid.Header.Set("Authorization", "Bearer gcp-tok")
	o.do(valid)

	haystack := o.logs.String() + "\n" + o.scrape()
	forbidden := []string{
		sentinel, "gh-secret", "gl-tok", "bb-secret", "pd-secret", "jira-secret",
		"aws-tok", "gcp-tok", "wrong-token-value", "not-the-secret", "core-tok",
		signed.Header.Get("X-Hub-Signature-256"), bad.Header.Get("X-Hub-Signature-256"),
	}
	for _, value := range forbidden {
		if value != "" && strings.Contains(haystack, value) {
			t.Fatalf("telemetry leaked %q:\n%s", value, haystack)
		}
	}
	if strings.Count(o.logs.String(), "\n") != 4 {
		t.Fatalf("want one log line per request (4), got:\n%s", o.logs.String())
	}
}

// TestTelemetryTruncatesHostileDeliveryID proves an attacker-sized delivery
// header cannot bloat a log line.
func TestTelemetryTruncatesHostileDeliveryID(t *testing.T) {
	t.Parallel()

	o := newObserved(t, nil)
	huge := strings.Repeat("d", 5000)
	o.do(githubRequest("wrong", "push", huge, githubPushPayload))

	id, _ := o.logLine()["delivery_id"].(string)
	if len(id) != 200 {
		t.Fatalf("logged delivery_id length = %d, want 200", len(id))
	}
}

// TestTelemetrySignedPingIsIgnoredNotAccepted proves a signed ping is
// counted as ignored/ping and logged at INFO, distinct from a submission
// handed to core.
func TestTelemetrySignedPingIsIgnoredNotAccepted(t *testing.T) {
	t.Parallel()

	o := newObserved(t, nil)
	rec := o.do(githubRequest("gh-secret", "ping", "d-ping", `{"zen":"hi"}`))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}

	o.wantLog(map[string]any{
		"level":       "INFO",
		"status":      float64(202),
		"outcome":     "ignored",
		"reason":      "ping",
		"delivery_id": "d-ping",
	})
	o.wantSeries(`eshu_webhook_requests_total{provider="github",outcome="ignored",reason="ping"} 1`)
	if len(o.core.submitted) != 0 {
		t.Fatalf("intake calls = %d, want 0", len(o.core.submitted))
	}
}
