// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// errReader fails every read so handler body-error branches stay covered
// without a network fault.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func (errReader) Close() error { return nil }

func signSHA256(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func signV1(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

// fullTestConfig enables every provider route so one server covers all
// handler failure branches.
func fullTestConfig() Config {
	cfg := testConfig()
	cfg.GitHubSecret = "gh-secret"
	cfg.GitLabToken = "gl-tok"
	cfg.BitbucketSecret = "bb-secret"
	cfg.PagerDutySecret = "pd-secret"
	cfg.PagerDutyScopeID = "scope-1"
	cfg.JiraSecret = "jira-secret"
	cfg.JiraScopeID = "scope-1"
	cfg.AWSFreshnessToken = "aws-tok"
	cfg.GCPFreshnessToken = "gcp-tok"
	return cfg
}

func serveMux(t *testing.T, cfg Config) (*Server, *stubCore) {
	t.Helper()
	stub := &stubCore{t: t}
	core := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(core.Close)
	cfg.IntakeURL = core.URL
	cfg.IntakeToken = "core-tok"
	srv, err := NewServerWithClient(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	return srv, stub
}

// TestCoverageDuplicateCount pins the receipt duplicate normalization:
// only a duplicate receipt reports a positive count, minimum 1.
func TestCoverageDuplicateCount(t *testing.T) {
	t.Parallel()

	if got := DuplicateCount(Receipt{}); got != 0 {
		t.Fatalf("DuplicateCount(zero) = %d, want 0", got)
	}
	if got := DuplicateCount(Receipt{Duplicate: true}); got != 1 {
		t.Fatalf("DuplicateCount(dup,0) = %d, want 1", got)
	}
	if got := DuplicateCount(Receipt{Duplicate: true, DuplicateCount: 5}); got != 5 {
		t.Fatalf("DuplicateCount(dup,5) = %d, want 5", got)
	}
}

// TestCoverageHandlersRejectBadCredentials proves every route answers 401
// without an upstream call when verification fails.
func TestCoverageHandlersRejectBadCredentials(t *testing.T) {
	t.Parallel()

	srv, stub := serveMux(t, fullTestConfig())
	mux := srv.Mux()
	cases := []struct {
		name   string
		path   string
		header map[string]string
	}{
		{"github", "/webhooks/github", map[string]string{"X-Hub-Signature-256": "sha256=00", "X-GitHub-Event": "push"}},
		{"gitlab", "/webhooks/gitlab", map[string]string{"X-Gitlab-Token": "wrong"}},
		{"bitbucket", "/webhooks/bitbucket", map[string]string{"X-Hub-Signature": "sha256=00", "X-Event-Key": "repo:push"}},
		{"pagerduty", "/webhooks/pagerduty", map[string]string{"X-PagerDuty-Signature": "v1=00"}},
		{"jira", "/webhooks/jira", map[string]string{"X-Hub-Signature": "sha256=00"}},
		{"aws", "/webhooks/aws/eventbridge", map[string]string{"Authorization": "Bearer wrong"}},
		{"gcp", "/webhook/gcp-freshness", map[string]string{"Authorization": "Bearer wrong"}},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(`{"x":1}`))
		for k, v := range tc.header {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401 (body %s)", tc.name, rec.Code, rec.Body.String())
		}
	}
	if len(stub.submitted) != 0 {
		t.Fatalf("rejected deliveries submitted %d requests, want 0", len(stub.submitted))
	}
}

// TestCoverageHandlersRejectInvalidPayload proves every route answers 422
// when verification passes but normalization fails.
func TestCoverageHandlersRejectInvalidPayload(t *testing.T) {
	t.Parallel()

	cfg := fullTestConfig()
	srv, stub := serveMux(t, cfg)
	mux := srv.Mux()
	garbage := []byte(`{"not":"a delivery"}`)
	cases := []struct {
		name   string
		path   string
		header map[string]string
	}{
		{"github", "/webhooks/github", map[string]string{
			"X-Hub-Signature-256": signSHA256(cfg.GitHubSecret, garbage), "X-GitHub-Event": "push",
		}},
		{"gitlab", "/webhooks/gitlab", map[string]string{"X-Gitlab-Token": cfg.GitLabToken}},
		{"bitbucket", "/webhooks/bitbucket", map[string]string{
			"X-Hub-Signature": signSHA256(cfg.BitbucketSecret, garbage), "X-Event-Key": "repo:push",
		}},
		{"pagerduty", "/webhooks/pagerduty", map[string]string{"X-PagerDuty-Signature": signV1(cfg.PagerDutySecret, garbage)}},
		{"jira", "/webhooks/jira", map[string]string{"X-Hub-Signature": signSHA256(cfg.JiraSecret, garbage)}},
		{"aws", "/webhooks/aws/eventbridge", map[string]string{"Authorization": "Bearer " + cfg.AWSFreshnessToken}},
		{"gcp", "/webhook/gcp-freshness", map[string]string{"Authorization": "Bearer " + cfg.GCPFreshnessToken}},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(string(garbage)))
		for k, v := range tc.header {
			req.Header.Set(k, v)
		}
		// Fresh copies: garbage is valid JSON that no normalizer accepts.
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d, want 422 (body %s)", tc.name, rec.Code, rec.Body.String())
		}
	}
	if len(stub.submitted) != 0 {
		t.Fatalf("unnormalizable deliveries submitted %d requests, want 0", len(stub.submitted))
	}
}

// TestCoverageReadPostBodyEdges proves the shared body gate: non-POST is
// 405, oversize is 413, and an unreadable body is a 400 without verification.
func TestCoverageReadPostBodyEdges(t *testing.T) {
	t.Parallel()

	srv, _ := serveMux(t, fullTestConfig())
	mux := srv.Mux()

	get := httptest.NewRequest(http.MethodGet, "/webhooks/github", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, get)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", rec.Code)
	}

	small := fullTestConfig()
	small.MaxRequestBodyBytes = 16
	srvSmall, _ := serveMux(t, small)
	big := httptest.NewRequest(http.MethodPost, "/webhooks/github",
		strings.NewReader(strings.Repeat("b", 17)))
	rec = httptest.NewRecorder()
	srvSmall.Mux().ServeHTTP(rec, big)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize status = %d, want 413", rec.Code)
	}

	broken := httptest.NewRequest(http.MethodPost, "/webhooks/github", nil)
	broken.Body = errReader{}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, broken)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unreadable status = %d, want 400", rec.Code)
	}
}

// TestCoverageRegisterSkipsEmptyPath proves an unconfigured provider
// exposes no surface instead of panicking.
func TestCoverageRegisterSkipsEmptyPath(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	register(mux, "  ", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nowhere", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for unregistered path", rec.Code)
	}
}

// TestCoverageReadyzEdges proves the readiness probe fails closed: an
// unreachable uplink, an unhealthy uplink, and an unbuildable probe request
// each answer 503.
func TestCoverageReadyzEdges(t *testing.T) {
	t.Parallel()

	fast := &http.Client{Timeout: 2 * time.Second}

	cfg := fullTestConfig() // IntakeURL http://127.0.0.1:1 refuses connections.
	srv, err := NewServerWithClient(cfg, fast)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unreachable status = %d, want 503", rec.Code)
	}

	ill := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ill.Close()
	cfg.IntakeURL = ill.URL
	srv, err = NewServerWithClient(cfg, fast)
	if err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unhealthy status = %d, want 503", rec.Code)
	}

	bad := &Server{cfg: fullTestConfig(), submit: &IntakeClient{
		baseURL: "http://%", token: "t", client: fast,
	}}
	rec = httptest.NewRecorder()
	bad.Mux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("bad URL status = %d, want 503", rec.Code)
	}
}

// TestCoverageSubmitErrorBranches proves the intake client maps every
// failure to an error callers can distinguish: unmarshalable requests,
// unbuildable requests, transport failures, unreadable bodies, unexpected
// statuses, undecodable receipts, and triggerless non-actions receipts.
func TestCoverageSubmitErrorBranches(t *testing.T) {
	t.Parallel()

	okCore := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"api_version":"v1","trigger_id":"t-1"}`))
	}))
	defer okCore.Close()
	newClient := func(t *testing.T, url string) *IntakeClient {
		t.Helper()
		c, err := NewIntakeClient(url, "tok")
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	if _, err := newClient(t, okCore.URL).Submit(t.Context(), "/r", func() {}); err == nil {
		t.Error("Submit(unmarshalable) = nil, want encode error")
	}
	badURL := &IntakeClient{baseURL: "http://%", token: "t", client: fastClient()}
	if _, err := badURL.Submit(t.Context(), "/r", map[string]string{"a": "b"}); err == nil {
		t.Error("Submit(bad URL) = nil, want build error")
	}
	shut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	shut.Close()
	if _, err := newClient(t, shut.URL).Submit(t.Context(), "/r", map[string]string{"a": "b"}); err == nil {
		t.Error("Submit(refused) = nil, want transport error")
	}

	short := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, _, _ := w.(http.Hijacker).Hijack()
		_, _ = fmt.Fprintf(h, "HTTP/1.1 200 OK\r\nContent-Length: 100\r\nContent-Type: application/json\r\n\r\nshort")
		_ = h.Close()
	}))
	defer short.Close()
	if _, err := newClient(t, short.URL).Submit(t.Context(), "/r", map[string]string{"a": "b"}); err == nil {
		t.Error("Submit(short body) = nil, want read error")
	}

	statusCases := []struct {
		name   string
		status int
		body   string
	}{
		{"conflict-other", http.StatusConflict, `{"code":"other"}`},
		{"server-error", http.StatusBadGateway, `oops`},
		{"bad-json", http.StatusOK, `not json`},
	}
	for _, tc := range statusCases {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		defer s.Close()
		if _, err := newClient(t, s.URL).Submit(t.Context(), "/r", map[string]string{"a": "b"}); err == nil {
			t.Errorf("Submit(%s) = nil, want error", tc.name)
		}
	}

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"api_version":"v1"}`))
	}))
	defer empty.Close()
	if _, err := newClient(t, empty.URL).Submit(t.Context(), IntakeRoutes["repository"], map[string]string{}); err == nil {
		t.Error("Submit(triggerless repository) = nil, want missing trigger error")
	}
	if _, err := newClient(t, empty.URL).Submit(t.Context(), IntakeRoutes["actions"], map[string]string{}); err != nil {
		t.Errorf("Submit(triggerless actions) = %v, want nil (actions exemption)", err)
	}
}

func fastClient() *http.Client { return &http.Client{Timeout: 2 * time.Second} }
