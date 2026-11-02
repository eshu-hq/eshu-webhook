// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestTelemetryGCPWelcomeIsIgnoredNotRejected proves the welcome message is
// counted as ignored/welcome_message at INFO, not as a 422 rejection an
// operator would chase.
func TestTelemetryGCPWelcomeIsIgnoredNotRejected(t *testing.T) {
	t.Parallel()

	o := newObserved(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/webhook/gcp-freshness",
		bytes.NewReader(readGCPFixture(t, "welcome.push.json")))
	req.Header.Set("Authorization", "Bearer gcp-tok")
	rec := o.do(req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	o.wantLog(map[string]any{
		"level":   "INFO",
		"status":  float64(202),
		"outcome": "ignored",
		"reason":  "welcome_message",
	})
	o.wantSeries(`eshu_webhook_requests_total{provider="gcp",outcome="ignored",reason="welcome_message"} 1`)
	if len(o.core.submitted) != 0 {
		t.Fatalf("intake calls = %d, want 0", len(o.core.submitted))
	}
}
