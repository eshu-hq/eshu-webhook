// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestServerBitbucketRoundTrip proves the HMAC provider path for Bitbucket:
// X-Event-Key verification, first-change normalization, intake submission.
func TestServerBitbucketRoundTrip(t *testing.T) {
	t.Parallel()

	stub := &stubCore{t: t}
	core := httptest.NewServer(http.HandlerFunc(stub.handler))
	defer core.Close()

	cfg := testConfig()
	cfg.BitbucketSecret = "bb-secret"
	cfg.IntakeURL = core.URL
	cfg.IntakeToken = "core-tok"
	srv, err := NewServerWithClient(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	payload := `{"repository":{"uuid":"{u1}","full_name":"team/repo","mainbranch":{"name":"main"}},` +
		`"push":{"changes":[{"new":{"type":"branch","name":"main","target":{"hash":"h1"}},` +
		`"old":{"target":{"hash":"h0"}}}]},"actor":{"nickname":"a"}}`
	mac := hmac.New(sha256.New, []byte("bb-secret"))
	mac.Write([]byte(payload))
	req := httptest.NewRequest(http.MethodPost, "/webhooks/bitbucket", strings.NewReader(payload))
	req.Header.Set("X-Event-Key", "repo:push")
	req.Header.Set("X-Request-UUID", "{req-1}")
	req.Header.Set("X-Hub-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}
	if len(stub.submitted) != 1 || stub.submitted[0].route != "/api/v1/webhook/intake/repository" {
		t.Fatalf("submissions = %+v", stub.submitted)
	}
	if !strings.Contains(string(stub.submitted[0].body), `"delivery_id":"req-1"`) {
		t.Fatalf("body missing brace-stripped delivery id: %s", stub.submitted[0].body)
	}
}

// TestServerFreshnessBearerPaths proves the token-auth freshness routes:
// AWS EventBridge and GCP Pub/Sub push deliveries submit to their intake
// families, and a wrong token is rejected without an upstream call.
func TestServerFreshnessBearerPaths(t *testing.T) {
	t.Parallel()

	stub := &stubCore{t: t}
	core := httptest.NewServer(http.HandlerFunc(stub.handler))
	defer core.Close()

	cfg := testConfig()
	cfg.AWSFreshnessToken = "aws-tok"
	cfg.GCPFreshnessToken = "gcp-tok"
	cfg.IntakeURL = core.URL
	cfg.IntakeToken = "core-tok"
	srv, err := NewServerWithClient(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	mux := srv.Mux()

	aws := `{"id":"ev-1","detail-type":"EC2 State Change","account":"1","region":"r",` +
		`"source":"aws.ec2","detail":{}}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/aws/eventbridge", strings.NewReader(aws))
	req.Header.Set("Authorization", "Bearer aws-tok")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("aws status = %d, want 202 (%s)", rec.Code, rec.Body.String())
	}

	gcp := `{"message":{"messageId":"m-1","attributes":{"parentScopeId":"p1"}}}`
	req = httptest.NewRequest(http.MethodPost, "/webhook/gcp-freshness", strings.NewReader(gcp))
	req.Header.Set("Authorization", "Bearer wrong")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("gcp bad-token status = %d, want 401", rec.Code)
	}
	if len(stub.submitted) != 1 || stub.submitted[0].route != "/api/v1/webhook/intake/aws" {
		t.Fatalf("submissions = %+v, want one aws intake", stub.submitted)
	}
}

// TestServerIncidentPaths proves the incident family routes: PagerDuty and
// Jira deliveries submit to the incident intake route.
func TestServerIncidentPaths(t *testing.T) {
	t.Parallel()

	stub := &stubCore{t: t}
	core := httptest.NewServer(http.HandlerFunc(stub.handler))
	defer core.Close()

	cfg := testConfig()
	cfg.PagerDutySecret = "pd"
	cfg.JiraSecret = "j"
	cfg.PagerDutyScopeID = "s1"
	cfg.JiraScopeID = "s2"
	cfg.IntakeURL = core.URL
	cfg.IntakeToken = "core-tok"
	srv, err := NewServerWithClient(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	mux := srv.Mux()

	pdPayload := `{"event":{"id":"e1","event_type":"incident.triggered"},"incident":{"id":"i1"}}`
	mac := hmac.New(sha256.New, []byte("pd"))
	mac.Write([]byte(pdPayload))
	req := httptest.NewRequest(http.MethodPost, "/webhooks/pagerduty", strings.NewReader(pdPayload))
	req.Header.Set("X-PagerDuty-Signature", "v1="+hex.EncodeToString(mac.Sum(nil)))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("pagerduty status = %d, want 202 (%s)", rec.Code, rec.Body.String())
	}

	jiraPayload := `{"webhookEvent":"jira:issue_updated","issue":{"id":"1","key":"K-1"}}`
	mac = hmac.New(sha256.New, []byte("j"))
	mac.Write([]byte(jiraPayload))
	req = httptest.NewRequest(http.MethodPost, "/webhooks/jira", strings.NewReader(jiraPayload))
	req.Header.Set("X-Hub-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("jira status = %d, want 202 (%s)", rec.Code, rec.Body.String())
	}
	if len(stub.submitted) != 2 {
		t.Fatalf("submissions = %d, want 2", len(stub.submitted))
	}
	for _, sub := range stub.submitted {
		if sub.route != "/api/v1/webhook/intake/incident" {
			t.Fatalf("route = %q, want incident intake", sub.route)
		}
	}
}

// TestServerOversizeBodyIs413 proves the HTTP path enforces the cap: a
// delivery one byte over returns 413 without contacting core.
func TestServerOversizeBodyIs413(t *testing.T) {
	t.Parallel()

	stub := &stubCore{t: t}
	core := httptest.NewServer(http.HandlerFunc(stub.handler))
	defer core.Close()

	cfg := testConfig()
	cfg.IntakeURL = core.URL
	cfg.IntakeToken = "core-tok"
	srv, err := NewServerWithClient(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	over := strings.Repeat("x", int(cfg.MaxRequestBodyBytes)+1)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(over))
	req.Header.Set("X-GitHub-Event", "push")
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	if len(stub.submitted) != 0 {
		t.Fatalf("submissions = %d, want 0", len(stub.submitted))
	}
}

// TestServerUnknownEventIs422 proves an unsupported event is rejected with
// 422 after successful verification (no silent drop, no submission).
func TestServerUnknownEventIs422(t *testing.T) {
	t.Parallel()

	stub := &stubCore{t: t}
	core := httptest.NewServer(http.HandlerFunc(stub.handler))
	defer core.Close()

	cfg := testConfig()
	cfg.GitHubSecret = "gh-secret"
	cfg.IntakeURL = core.URL
	cfg.IntakeToken = "core-tok"
	srv, err := NewServerWithClient(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	payload := `{"zen":"hi"}`
	mac := hmac.New(sha256.New, []byte("gh-secret"))
	mac.Write([]byte(payload))
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "ping")
	req.Header.Set("X-GitHub-Delivery", "d-ping")
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if len(stub.submitted) != 0 {
		t.Fatalf("submissions = %d, want 0", len(stub.submitted))
	}
}

// TestServerCoreFailureIs502 proves a core 500 becomes a 502 so the
// provider redelivers instead of the delivery being silently lost.
func TestServerCoreFailureIs502(t *testing.T) {
	t.Parallel()

	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer core.Close()

	cfg := testConfig()
	cfg.GitHubSecret = "gh-secret"
	cfg.IntakeURL = core.URL
	cfg.IntakeToken = "core-tok"
	srv, err := NewServerWithClient(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	mac := hmac.New(sha256.New, []byte("gh-secret"))
	mac.Write([]byte(githubPushPayload))
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(githubPushPayload))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "d-err")
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

// TestServerReadyzReflectsUplink proves readiness follows core: healthy
// uplink is 200, unreachable uplink is 503.
func TestServerReadyzReflectsUplink(t *testing.T) {
	t.Parallel()

	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer healthy.Close()

	cfg := testConfig()
	cfg.IntakeURL = healthy.URL
	cfg.IntakeToken = "tok"
	srv, err := NewServerWithClient(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("ready status = %d, want 200", rec.Code)
	}

	down := testConfig()
	down.IntakeURL = "http://127.0.0.1:1"
	down.IntakeToken = "tok"
	srvDown, err := NewServerWithClient(down, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	srvDown.Mux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unready status = %d, want 503", rec.Code)
	}
}

// TestDeliveryIDFallbackHash proves deliveries without a provider delivery
// header still carry a stable content-derived idempotency key.
func TestDeliveryIDFallbackHash(t *testing.T) {
	t.Parallel()

	a := deliveryID("", []byte("same"))
	b := deliveryID("", []byte("same"))
	c := deliveryID("", []byte("different"))
	if a != b || a == c {
		t.Fatal("fallback hash must be stable per content")
	}
	if got := deliveryID("{req-uuid}", nil); got != "req-uuid" {
		t.Fatalf("deliveryID braces = %q", got)
	}
	if got := bearerToken("Bearer abc"); got != "abc" {
		t.Fatalf("bearerToken = %q", got)
	}
	if got := bearerToken("Token abc"); got != "" {
		t.Fatalf("bearerToken(non-bearer) = %q, want empty", got)
	}
}
