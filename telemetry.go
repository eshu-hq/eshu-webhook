// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Metric family names served on /metrics. The satellite uses the
// eshu_webhook_ namespace so its series never collide with the intake host's
// eshu_dp_webhook_* series, which describe a different hop.
const (
	metricRequestsTotal    = "eshu_webhook_requests_total"
	metricRequestDuration  = "eshu_webhook_request_duration_seconds"
	metricsContentType     = "text/plain; version=0.0.4; charset=utf-8"
	requestsTotalHelp      = "Provider webhook requests handled, by provider, outcome, and machine reason."
	requestDurationHelp    = "Provider webhook request handling time in seconds, by provider."
	outcomeAccepted        = "accepted"
	outcomeIgnored         = "ignored"
	outcomeRejected        = "rejected"
	outcomeFailed          = "failed"
	reasonAccepted         = "accepted"
	reasonBadSignature     = "bad_signature"
	reasonBadToken         = "bad_token"
	reasonOversizedBody    = "oversized_body"
	reasonUnreadableBody   = "unreadable_body"
	reasonMethodNotAllowed = "method_not_allowed"
	reasonInvalidPayload   = "invalid_payload"
	reasonDeliveryConflict = "delivery_conflict"
	reasonIntakeFailed     = "intake_failed"
	reasonPanic            = "panic"
	reasonOther            = "other"
)

// durationBuckets are the upper bounds, in seconds, of the request duration
// histogram. They span a local reject (microseconds) to the bounded intake
// uplink timeout.
var durationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// requestKey identifies one requests_total series. Every field takes a
// value from a closed set chosen by the server, never from request content,
// so series cardinality is bounded by providers x outcomes x reasons.
type requestKey struct {
	provider string
	outcome  string
	reason   string
}

// durationHistogram is one provider's cumulative-on-render duration
// histogram. counts[i] holds observations at or below durationBuckets[i]
// and not below the previous bound; the +Inf bucket is derived from count.
type durationHistogram struct {
	counts []uint64
	count  uint64
	sum    float64
}

// metrics is the satellite's in-process request telemetry. It is stdlib
// only because the import boundary admits no third-party module; the text
// exposition format 0.0.4 it writes is what a Prometheus scrape expects.
type metrics struct {
	mu        sync.Mutex
	requests  map[requestKey]uint64
	durations map[string]*durationHistogram
}

func newMetrics() *metrics {
	return &metrics{
		requests:  map[requestKey]uint64{},
		durations: map[string]*durationHistogram{},
	}
}

// observe records one finished provider request.
func (m *metrics) observe(provider, outcome, reason string, elapsed time.Duration) {
	seconds := elapsed.Seconds()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests[requestKey{provider: provider, outcome: outcome, reason: reason}]++
	hist, ok := m.durations[provider]
	if !ok {
		hist = &durationHistogram{counts: make([]uint64, len(durationBuckets))}
		m.durations[provider] = hist
	}
	hist.count++
	hist.sum += seconds
	for i, bound := range durationBuckets {
		if seconds <= bound {
			hist.counts[i]++
			break
		}
	}
}

// writeText renders the Prometheus text exposition, with series sorted so
// scrapes and tests are deterministic. Both family headers are always
// written so an idle process still exposes a parseable document.
//
// The document is rendered into a buffer while the lock is held and written
// to w only after the lock is released, so a slow or stalled scrape client
// can never block observe on the request path.
func (m *metrics) writeText(w io.Writer) {
	var buf bytes.Buffer
	m.mu.Lock()
	m.render(&buf)
	m.mu.Unlock()
	_, _ = w.Write(buf.Bytes())
}

// render writes the exposition to an in-memory buffer. The caller holds m.mu.
func (m *metrics) render(w *bytes.Buffer) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n", metricRequestsTotal, requestsTotalHelp, metricRequestsTotal)
	keys := make([]requestKey, 0, len(m.requests))
	for key := range m.requests {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.provider != b.provider {
			return a.provider < b.provider
		}
		if a.outcome != b.outcome {
			return a.outcome < b.outcome
		}
		return a.reason < b.reason
	})
	for _, key := range keys {
		fmt.Fprintf(w, "%s{provider=%q,outcome=%q,reason=%q} %d\n",
			metricRequestsTotal, key.provider, key.outcome, key.reason, m.requests[key])
	}

	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s histogram\n", metricRequestDuration, requestDurationHelp, metricRequestDuration)
	providers := make([]string, 0, len(m.durations))
	for provider := range m.durations {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	for _, provider := range providers {
		hist := m.durations[provider]
		var cumulative uint64
		for i, bound := range durationBuckets {
			cumulative += hist.counts[i]
			fmt.Fprintf(w, "%s_bucket{provider=%q,le=%q} %d\n",
				metricRequestDuration, provider, strconv.FormatFloat(bound, 'g', -1, 64), cumulative)
		}
		fmt.Fprintf(w, "%s_bucket{provider=%q,le=\"+Inf\"} %d\n", metricRequestDuration, provider, hist.count)
		fmt.Fprintf(w, "%s_sum{provider=%q} %s\n", metricRequestDuration, provider, strconv.FormatFloat(hist.sum, 'g', -1, 64))
		fmt.Fprintf(w, "%s_count{provider=%q} %d\n", metricRequestDuration, provider, hist.count)
	}
}
