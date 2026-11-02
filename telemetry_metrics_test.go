// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestTelemetryHealthProbesAreNotRequests keeps liveness and readiness
// traffic out of the provider series and the request log.
func TestTelemetryHealthProbesAreNotRequests(t *testing.T) {
	t.Parallel()

	o := newObserved(t, nil)
	o.do(httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if o.logs.Len() != 0 {
		t.Fatalf("healthz wrote a request log: %s", o.logs.String())
	}
	if text := o.scrape(); strings.Contains(text, "eshu_webhook_requests_total{") {
		t.Fatalf("healthz produced a requests_total series:\n%s", text)
	}
}

// TestTelemetryCountersAccumulateAndHistogramIsCumulative proves repeated
// requests add up and the duration histogram exposes cumulative buckets
// ending in +Inf equal to the count.
func TestTelemetryCountersAccumulateAndHistogramIsCumulative(t *testing.T) {
	t.Parallel()

	o := newObserved(t, nil)
	for i := 0; i < 3; i++ {
		o.do(githubRequest("wrong", "push", "d", githubPushPayload))
	}
	o.logs.Reset()
	o.do(githubRequest("gh-secret", "push", "d-ok", githubPushPayload))

	o.wantSeries(`eshu_webhook_requests_total{provider="github",outcome="rejected",reason="bad_signature"} 3`)
	o.wantSeries(`eshu_webhook_requests_total{provider="github",outcome="accepted",reason="accepted"} 1`)
	o.wantSeries(`eshu_webhook_request_duration_seconds_bucket{provider="github",le="+Inf"} 4`)
	o.wantSeries(`eshu_webhook_request_duration_seconds_count{provider="github"} 4`)
	text := o.scrape()
	for _, want := range []string{
		"# TYPE eshu_webhook_requests_total counter",
		"# TYPE eshu_webhook_request_duration_seconds histogram",
		"# HELP eshu_webhook_requests_total ",
		"# HELP eshu_webhook_request_duration_seconds ",
		"eshu_webhook_request_duration_seconds_sum{provider=\"github\"} ",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("/metrics missing %q:\n%s", want, text)
		}
	}
}

// TestTelemetryHandlerPanicIsCountedThenRepanics proves a panicking handler
// leaves a failed/panic series and log line, and the panic still propagates
// to net/http's own recovery.
func TestTelemetryHandlerPanicIsCountedThenRepanics(t *testing.T) {
	t.Parallel()

	o := newObserved(t, nil)
	handler := o.srv.instrument("github", func(http.ResponseWriter, *http.Request) { panic("boom") })
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("instrument swallowed the handler panic")
			}
		}()
		handler(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/webhooks/github", nil))
	}()

	o.wantLog(map[string]any{"level": "ERROR", "outcome": "failed", "reason": "panic", "status": float64(500)})
	o.wantSeries(`eshu_webhook_requests_total{provider="github",outcome="failed",reason="panic"} 1`)
}

// TestMetricsEndpointRejectsNonRead proves /metrics is read-only.
func TestMetricsEndpointRejectsNonRead(t *testing.T) {
	t.Parallel()

	o := newObserved(t, nil)
	rec := o.do(httptest.NewRequest(http.MethodPost, "/metrics", strings.NewReader("x")))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /metrics status = %d, want 405", rec.Code)
	}
}

// TestMetricsEmptyServerStillServesFamilies proves a fresh process serves a
// valid, parseable exposition (HELP/TYPE headers) before any traffic.
func TestMetricsEmptyServerStillServesFamilies(t *testing.T) {
	t.Parallel()

	o := newObserved(t, nil)
	text := o.scrape()
	if !strings.Contains(text, "# TYPE eshu_webhook_requests_total counter") {
		t.Fatalf("empty exposition missing TYPE header:\n%s", text)
	}
}

// TestMetricsHeadServesHeadersOnly proves HEAD /metrics is allowed and
// carries no body.
func TestMetricsHeadServesHeadersOnly(t *testing.T) {
	t.Parallel()

	o := newObserved(t, nil)
	rec := o.do(httptest.NewRequest(http.MethodHead, "/metrics", nil))
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("HEAD /metrics = %d with %d body bytes, want 200 and 0", rec.Code, rec.Body.Len())
	}
}

// TestTelemetryClassifiesUnmappedStatuses proves a handler that writes a
// body without a status counts as 200 accepted, and an unmapped 4xx falls to
// the closed "other" reason instead of inventing a new label value.
func TestTelemetryClassifiesUnmappedStatuses(t *testing.T) {
	t.Parallel()

	o := newObserved(t, nil)
	implicit := o.srv.instrument("github", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	implicit(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/webhooks/github", nil))
	teapot := o.srv.instrument("github", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	teapot(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/webhooks/github", nil))

	o.wantSeries(`eshu_webhook_requests_total{provider="github",outcome="accepted",reason="other"} 1`)
	o.wantSeries(`eshu_webhook_requests_total{provider="github",outcome="rejected",reason="other"} 1`)
}

// TestMetricsHistogramBucketsAreCumulative observes known durations and
// checks each bucket counts everything at or below its bound.
func TestMetricsHistogramBucketsAreCumulative(t *testing.T) {
	t.Parallel()

	m := newMetrics()
	m.observe("github", outcomeAccepted, reasonAccepted, 1*time.Millisecond)
	m.observe("github", outcomeAccepted, reasonAccepted, 20*time.Millisecond)
	m.observe("github", outcomeAccepted, reasonAccepted, 3*time.Second)
	m.observe("github", outcomeAccepted, reasonAccepted, 60*time.Second)
	var out strings.Builder
	m.writeText(&out)
	text := out.String()
	for _, want := range []string{
		`eshu_webhook_request_duration_seconds_bucket{provider="github",le="0.005"} 1`,
		`eshu_webhook_request_duration_seconds_bucket{provider="github",le="0.025"} 2`,
		`eshu_webhook_request_duration_seconds_bucket{provider="github",le="2.5"} 2`,
		`eshu_webhook_request_duration_seconds_bucket{provider="github",le="5"} 3`,
		`eshu_webhook_request_duration_seconds_bucket{provider="github",le="10"} 3`,
		`eshu_webhook_request_duration_seconds_bucket{provider="github",le="+Inf"} 4`,
		`eshu_webhook_request_duration_seconds_count{provider="github"} 4`,
		`eshu_webhook_request_duration_seconds_sum{provider="github"} 63.021`,
	} {
		if !strings.Contains(text, want+"\n") {
			t.Fatalf("exposition missing %q:\n%s", want, text)
		}
	}
}

// TestTelemetryConcurrentRequestsAreCountedExactly drives rejected requests
// from many goroutines (run under -race) and proves no increment is lost.
func TestTelemetryConcurrentRequestsAreCountedExactly(t *testing.T) {
	t.Parallel()

	o := newObserved(t, nil)
	o.srv.WithLogger(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	const workers, each = 16, 25
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < each; j++ {
				o.srv.Mux().ServeHTTP(httptest.NewRecorder(), githubRequest("wrong", "push", "d", githubPushPayload))
			}
		}()
	}
	wg.Wait()

	o.wantSeries(`eshu_webhook_requests_total{provider="github",outcome="rejected",reason="bad_signature"} 400`)
	o.wantSeries(`eshu_webhook_request_duration_seconds_count{provider="github"} 400`)
}
