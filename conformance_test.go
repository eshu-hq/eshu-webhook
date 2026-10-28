// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubCore records the intake submissions the satellite makes and answers
// with canned receipts.
type stubCore struct {
	t         *testing.T
	submitted []stubSubmission
	conflict  bool
}

type stubSubmission struct {
	route string
	auth  string
	body  []byte
}

func (s *stubCore) handler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	s.submitted = append(s.submitted, stubSubmission{route: r.URL.Path, auth: r.Header.Get("Authorization"), body: body})
	w.Header().Set("Content-Type", "application/json")
	if s.conflict {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"api_version":"v1","code":"delivery_conflict","duplicate":true}`))
		return
	}
	_, _ = w.Write([]byte(`{"api_version":"v1","trigger_id":"t-9","status":"pending","decision":"accepted","kind":"push"}`))
}

func signedGitHub(secret string, payload []byte) (string, string) {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "push", "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

const githubPushPayload = `{"ref":"refs/heads/main","before":"abc000","after":"def111",` +
	`"repository":{"id":42,"full_name":"octo/repo","default_branch":"main"},` +
	`"sender":{"login":"octo"},"head_commit":{"id":"def111"}}`

// TestConformanceGitHubPushEndToEnd proves the full satellite path for one
// provider: verify the provider signature, normalize the payload, and submit
// the v1 intake request with bearer auth to the exact core route.
func TestConformanceGitHubPushEndToEnd(t *testing.T) {
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
	mux := srv.Mux()

	event, sig := signedGitHub("gh-secret", []byte(githubPushPayload))
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(githubPushPayload))
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-GitHub-Delivery", "deliv-1")
	req.Header.Set("X-Hub-Signature-256", sig)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if len(stub.submitted) != 1 {
		t.Fatalf("core submissions = %d, want 1", len(stub.submitted))
	}
	sub := stub.submitted[0]
	if sub.route != "/api/v1/webhook/intake/repository" {
		t.Fatalf("route = %q, want intake repository route", sub.route)
	}
	if sub.auth != "Bearer core-tok" {
		t.Fatalf("auth = %q, want Bearer core-tok", sub.auth)
	}
	for _, want := range []string{`"delivery_id":"deliv-1"`, `"repository_full_name":"octo/repo"`, `"target_sha":"def111"`, `"decision":"accepted"`} {
		if !strings.Contains(string(sub.body), want) {
			t.Errorf("submitted body missing %s: %s", want, sub.body)
		}
	}
}

// TestConformanceBadSignatureNeverReachesCore proves a forged delivery is
// rejected with 401 and produces zero intake submissions.
func TestConformanceBadSignatureNeverReachesCore(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(githubPushPayload))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "deliv-2")
	req.Header.Set("X-Hub-Signature-256", "sha256=00")
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(stub.submitted) != 0 {
		t.Fatalf("core submissions = %d, want 0", len(stub.submitted))
	}
}

// TestConformanceNonPOSTIsRejected proves only POST reaches the verify path.
func TestConformanceNonPOSTIsRejected(t *testing.T) {
	t.Parallel()

	srv, err := NewServerWithClient(testConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/webhooks/github", nil)
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

// TestConformanceGitLabPushEndToEnd proves the token-auth provider path:
// X-Gitlab-Token verification, normalization, and intake submission.
func TestConformanceGitLabPushEndToEnd(t *testing.T) {
	t.Parallel()

	stub := &stubCore{t: t}
	core := httptest.NewServer(http.HandlerFunc(stub.handler))
	defer core.Close()

	cfg := testConfig()
	cfg.GitLabToken = "gl-tok"
	cfg.IntakeURL = core.URL
	cfg.IntakeToken = "core-tok"
	srv, err := NewServerWithClient(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	payload := `{"object_kind":"push","ref":"refs/heads/main","before":"a0","after":"b1",` +
		`"project":{"id":7,"path_with_namespace":"grp/proj","default_branch":"main"},` +
		`"user_username":"u","checkout_sha":"b1"}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/gitlab", strings.NewReader(payload))
	req.Header.Set("X-Gitlab-Event", "Push Hook")
	req.Header.Set("X-Gitlab-Token", "gl-tok")
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}
	if len(stub.submitted) != 1 || stub.submitted[0].route != "/api/v1/webhook/intake/repository" {
		t.Fatalf("submissions = %+v, want one repository intake", stub.submitted)
	}
}

// TestConformanceConflictMapsToConflict proves a core delivery-conflict
// 409 on a conflicting-identity Actions redelivery surfaces as 409
// (permanent), matching the in-tree listener contract — never as a 200
// that mislabels the conflict "duplicate".
func TestConformanceConflictMapsToConflict(t *testing.T) {
	t.Parallel()

	stub := &stubCore{t: t, conflict: true}
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

	payload := `{"action":"completed",` +
		`"repository":{"id":42,"full_name":"Octo/Repo"},` +
		`"workflow_run":{"id":100}}`
	_, sig := signedGitHub("gh-secret", []byte(payload))
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "workflow_run")
	req.Header.Set("X-GitHub-Delivery", "deliv-conflict")
	req.Header.Set("X-Hub-Signature-256", sig)
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 for conflicting identity (body %s)", rec.Code, rec.Body.String())
	}
	if len(stub.submitted) != 1 || stub.submitted[0].route != "/api/v1/webhook/intake/actions" {
		t.Fatalf("submissions = %+v, want one actions intake", stub.submitted)
	}
}

// TestConformanceConflictMessageNamesRoute proves the 409 body names the
// route that conflicted: a repository-route conflict must not answer with
// an actions-specific message.
func TestConformanceConflictMessageNamesRoute(t *testing.T) {
	t.Parallel()

	stub := &stubCore{t: t, conflict: true}
	core := httptest.NewServer(http.HandlerFunc(stub.handler))
	defer core.Close()

	cfg := testConfig()
	cfg.GitLabToken = "gl-tok"
	cfg.IntakeURL = core.URL
	cfg.IntakeToken = "core-tok"
	srv, err := NewServerWithClient(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	payload := `{"object_kind":"push","ref":"refs/heads/main","before":"a0","after":"b1",` +
		`"project":{"id":7,"path_with_namespace":"grp/proj","default_branch":"main"},` +
		`"user_username":"u","checkout_sha":"b1"}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/gitlab", strings.NewReader(payload))
	req.Header.Set("X-Gitlab-Event", "Push Hook")
	req.Header.Set("X-Gitlab-Token", "gl-tok")
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "actions") {
		t.Fatalf("409 body %s names actions on a repository route", rec.Body.String())
	}
}

// TestConflictMessageNamesEachRoute proves every intake route maps to its
// own 409 label, with unknown routes falling back to the repository label.
func TestConflictMessageNamesEachRoute(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		IntakeRoutes["repository"]:       "conflicting repository delivery",
		IntakeRoutes["actions"]:          "conflicting actions delivery",
		IntakeRoutes["incident"]:         "conflicting incident delivery",
		IntakeRoutes["aws"]:              "conflicting aws delivery",
		IntakeRoutes["gcp"]:              "conflicting gcp delivery",
		"/api/v1/webhook/intake/unknown": "conflicting repository delivery",
	}
	for route, want := range cases {
		if got := conflictMessage(route); got != want {
			t.Errorf("conflictMessage(%q) = %q, want %q", route, got, want)
		}
	}
}

// TestConformanceExactDuplicateMapsToAccepted proves a true duplicate
// (core 202 accepted) still surfaces as satellite 202.
func TestConformanceExactDuplicateMapsToAccepted(t *testing.T) {
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

	payload := `{"action":"completed",` +
		`"repository":{"id":42,"full_name":"Octo/Repo"},` +
		`"workflow_run":{"id":100}}`
	_, sig := signedGitHub("gh-secret", []byte(payload))
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "workflow_run")
	req.Header.Set("X-GitHub-Delivery", "deliv-exact")
	req.Header.Set("X-Hub-Signature-256", sig)
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 for exact duplicate (body %s)", rec.Code, rec.Body.String())
	}
}

// TestConformanceHealthz proves the local liveness probe answers without
// provider secrets or core reachability.
func TestConformanceHealthz(t *testing.T) {
	t.Parallel()

	srv, err := NewServerWithClient(testConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// TestConformanceCIWorkflowPinsRequiredLanes keeps the GitHub Actions
// contract stable: the required ci-complete aggregation plus the lint, test,
// race, shell, image, govulncheck, gosec, and actionlint lanes.
func TestConformanceCIWorkflowPinsRequiredLanes(t *testing.T) {
	t.Parallel()

	raw, err := readRepoFile(t, ".github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, lane := range []string{"lint:", "test:", "race:", "shell:", "image:", "govulncheck:", "gosec:", "actionlint:", "ci-complete:"} {
		if !strings.Contains(raw, lane) {
			t.Errorf("ci.yml missing lane %q", lane)
		}
	}
}

// TestConformanceCIWorkflowLanePathConditions pins the path-filter
// conditions that select each lane, not just the lane names. A lane that
// exists but never runs (or always runs) would pass the name check while
// docs-only PRs pay for Go lanes or Go PRs skip them.
func TestConformanceCIWorkflowLanePathConditions(t *testing.T) {
	t.Parallel()

	raw, err := readRepoFile(t, ".github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	blocks := splitWorkflowJobs(t, raw)
	for lane, want := range map[string][]string{
		// Each lane must gate on its filter class (plus misc/workflow
		// catch-alls where the workflow defines them).
		"lint:":        {"needs.changes.outputs.go", "needs.changes.outputs.misc", "needs.changes.outputs.workflows"},
		"test:":        {"needs.changes.outputs.go", "needs.changes.outputs.fixtures", "needs.changes.outputs.misc", "needs.changes.outputs.workflows"},
		"race:":        {"needs.changes.outputs.go", "needs.changes.outputs.misc", "needs.changes.outputs.workflows"},
		"shell:":       {"needs.changes.outputs.shell"},
		"image:":       {"needs.changes.outputs.image", "needs.changes.outputs.go", "needs.changes.outputs.workflows"},
		"govulncheck:": {"needs.changes.outputs.deps", "needs.changes.outputs.workflows"},
		"gosec:":       {"needs.changes.outputs.go", "needs.changes.outputs.workflows"},
		"actionlint:":  {"needs.changes.outputs.workflows"},
	} {
		block, ok := blocks[lane]
		if !ok {
			t.Errorf("ci.yml missing lane block %q", lane)
			continue
		}
		for _, cond := range want {
			if !strings.Contains(block, cond) {
				t.Errorf("ci.yml lane %q missing path condition %q", lane, cond)
			}
		}
	}
	// ci-complete must fail closed: a failure or cancellation anywhere
	// fails the aggregator, while legitimate skips stay green.
	agg, ok := blocks["ci-complete:"]
	if !ok {
		t.Fatal("ci.yml missing lane block \"ci-complete:\"")
	}
	for _, want := range []string{`"failure"`, `"cancelled"`} {
		if !strings.Contains(agg, want) {
			t.Errorf("ci.yml ci-complete aggregator missing %s result check", want)
		}
	}
}

// splitWorkflowJobs splits a workflow file into per-job blocks keyed by the
// job header ("  <name>:"). Blocks run to the next same-indent job header.
func splitWorkflowJobs(t *testing.T, raw string) map[string]string {
	t.Helper()

	blocks := map[string]string{}
	current := ""
	var sb strings.Builder
	flush := func() {
		if current != "" {
			blocks[current] = sb.String()
			sb.Reset()
		}
	}
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimRight(line, " \t")
		if strings.HasPrefix(trimmed, "  ") && !strings.HasPrefix(trimmed, "   ") &&
			strings.HasSuffix(trimmed, ":") && !strings.Contains(trimmed[2:], " ") &&
			!strings.Contains(trimmed, "${{") {
			flush()
			current = strings.TrimSpace(trimmed)
			sb.WriteString(trimmed + "\n")
			continue
		}
		if current != "" {
			sb.WriteString(trimmed + "\n")
		}
	}
	flush()
	return blocks
}
