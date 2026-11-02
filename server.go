// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// Server is the satellite's HTTP surface: provider webhook routes plus
// local liveness. Every provider route verifies, normalizes, and submits
// through the core intake API; nothing is stored locally.
type Server struct {
	cfg     Config
	submit  *IntakeClient
	logger  *slog.Logger
	metrics *metrics
}

// NewServer builds the satellite server. The intake uplink is always
// required: without core there is nowhere to submit, and the satellite must
// fail at startup rather than accept deliveries it cannot hand off. A nil
// http client selects the default bounded client.
func NewServer(cfg Config, client *http.Client) (*Server, error) {
	submit, err := newIntakeClientWithHTTP(cfg.IntakeURL, cfg.IntakeToken, client)
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, submit: submit, logger: discardLogger(), metrics: newMetrics()}, nil
}

// NewServerWithClient is NewServer with an explicit transport, used by
// tests to point the intake uplink at a stub core.
func NewServerWithClient(cfg Config, client *http.Client) (*Server, error) {
	return NewServer(cfg, client)
}

// Mux wires the provider routes from configuration plus the local health
// probe. Routes are registered exactly once; a zero path is skipped so an
// unconfigured provider exposes no surface.
func (s *Server) Mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)
	mux.HandleFunc("/metrics", s.handleMetrics)
	register(mux, s.cfg.GitHubPath, s.instrument("github", s.handleGitHub))
	register(mux, s.cfg.GitLabPath, s.instrument("gitlab", s.handleGitLab))
	register(mux, s.cfg.BitbucketPath, s.instrument("bitbucket", s.handleBitbucket))
	register(mux, s.cfg.PagerDutyPath, s.instrument("pagerduty", s.handlePagerDuty))
	register(mux, s.cfg.JiraPath, s.instrument("jira", s.handleJira))
	register(mux, s.cfg.AWSPath, s.instrument("aws", s.handleAWSFreshness))
	register(mux, s.cfg.GCPPath, s.instrument("gcp", s.handleGCPFreshness))
	return mux
}

func register(mux *http.ServeMux, path string, handler http.HandlerFunc) {
	if strings.TrimSpace(path) == "" {
		return
	}
	mux.HandleFunc(path, handler)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadyz probes core's /healthz. A satellite that cannot reach core
// is not ready: it could accept provider deliveries it cannot hand off, so
// traffic must stay on a replica that can.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, s.submit.baseURL+"/healthz", nil)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "intake uplink is not configured")
		return
	}
	// #nosec G107 -- the request URL is the operator-configured intake base
	// URL (validated to http(s) scheme plus host at construction) joined
	// with the /healthz probe path; no delivery content reaches the URL.
	response, err := s.submit.client.Do(request)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "intake uplink is unreachable")
		return
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		writeError(w, http.StatusServiceUnavailable, "intake uplink is unhealthy")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) handleGitHub(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readPostBody(w, r)
	if !ok {
		return
	}
	if err := VerifyGitHubSignature(body, s.cfg.GitHubSecret, r.Header.Get("X-Hub-Signature-256")); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid github signature")
		return
	}
	event := strings.TrimSpace(r.Header.Get("X-GitHub-Event"))
	if strings.EqualFold(event, githubEventPing) {
		writeGitHubPingAccepted(w)
		return
	}
	deliveryID := deliveryID(r.Header.Get("X-GitHub-Delivery"), body)
	switch event {
	case "workflow_run", "workflow_job":
		req, err := NormalizeGitHubActions(event, deliveryID, body)
		if err != nil {
			writeUnprocessable(w, err)
			return
		}
		s.submitRequest(w, r, IntakeRoutes["actions"], req)
	default:
		req, err := NormalizeGitHub(event, deliveryID, body, s.cfg.DefaultBranch)
		if err != nil {
			writeUnprocessable(w, err)
			return
		}
		s.submitRequest(w, r, IntakeRoutes["repository"], req)
	}
}

// githubEventPing is the event name GitHub sends when a webhook is created
// and on manual redelivery. It carries no repository change.
const githubEventPing = "ping"

// writeGitHubPingAccepted answers GitHub's webhook handshake exactly as the
// removed in-tree listener did: 202 with the ignored decision and reason
// "ping". The delivery is verified before this runs and is never submitted
// to the intake host, so nothing is stored for it.
func writeGitHubPingAccepted(w http.ResponseWriter) {
	setIgnored(w, githubEventPing)
	writeJSON(w, http.StatusAccepted, map[string]string{
		"status":   "ignored",
		"decision": "ignored",
		"reason":   githubEventPing,
	})
}

// gcpWelcomeReason is the machine reason for the Cloud Asset Inventory
// first-subscription welcome message.
const gcpWelcomeReason = "welcome_message"

// writeGCPWelcomeAccepted acknowledges the verified welcome message the way
// core's push handler does: 202 with status ignored and reason
// welcome_message, no intake call, so Pub/Sub does not redeliver it.
func writeGCPWelcomeAccepted(w http.ResponseWriter) {
	setIgnored(w, gcpWelcomeReason)
	writeJSON(w, http.StatusAccepted, map[string]string{
		"status": "ignored",
		"reason": gcpWelcomeReason,
	})
}

func (s *Server) handleGitLab(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readPostBody(w, r)
	if !ok {
		return
	}
	if err := VerifyGitLabToken(s.cfg.GitLabToken, r.Header.Get("X-Gitlab-Token")); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid gitlab token")
		return
	}
	req, err := NormalizeGitLab(r.Header.Get("X-Gitlab-Event"), deliveryID(r.Header.Get("X-Gitlab-Delivery"), body), body, s.cfg.DefaultBranch)
	if err != nil {
		writeUnprocessable(w, err)
		return
	}
	s.submitRequest(w, r, IntakeRoutes["repository"], req)
}

func (s *Server) handleBitbucket(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readPostBody(w, r)
	if !ok {
		return
	}
	if err := VerifyBitbucketSignature(body, s.cfg.BitbucketSecret, r.Header.Get("X-Hub-Signature")); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid bitbucket signature")
		return
	}
	req, err := NormalizeBitbucket(r.Header.Get("X-Event-Key"), deliveryID(r.Header.Get("X-Request-UUID"), body), body, s.cfg.DefaultBranch)
	if err != nil {
		writeUnprocessable(w, err)
		return
	}
	s.submitRequest(w, r, IntakeRoutes["repository"], req)
}

func (s *Server) handlePagerDuty(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readPostBody(w, r)
	if !ok {
		return
	}
	if err := VerifyPagerDutySignature(body, s.cfg.PagerDutySecret, r.Header.Get("X-PagerDuty-Signature")); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid pagerduty signature")
		return
	}
	req, err := NormalizePagerDuty(body, s.cfg.PagerDutyScopeID)
	if err != nil {
		writeUnprocessable(w, err)
		return
	}
	s.submitRequest(w, r, IntakeRoutes["incident"], req)
}

func (s *Server) handleJira(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readPostBody(w, r)
	if !ok {
		return
	}
	if err := VerifyJiraSignature(body, s.cfg.JiraSecret, r.Header.Get("X-Hub-Signature")); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid jira signature")
		return
	}
	req, err := NormalizeJira(body, s.cfg.JiraScopeID)
	if err != nil {
		writeUnprocessable(w, err)
		return
	}
	s.submitRequest(w, r, IntakeRoutes["incident"], req)
}

func (s *Server) handleAWSFreshness(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readPostBody(w, r)
	if !ok {
		return
	}
	if err := VerifyStaticToken(s.cfg.AWSFreshnessToken, bearerToken(r.Header.Get("Authorization"))); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid aws freshness token")
		return
	}
	req, err := NormalizeAWSFreshness(body)
	if err != nil {
		writeUnprocessable(w, err)
		return
	}
	s.submitRequest(w, r, IntakeRoutes["aws"], req)
}

func (s *Server) handleGCPFreshness(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readPostBody(w, r)
	if !ok {
		return
	}
	if err := VerifyStaticToken(s.cfg.GCPFreshnessToken, bearerToken(r.Header.Get("Authorization"))); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid gcp freshness token")
		return
	}
	req, err := NormalizeGCPFreshness(body)
	if errors.Is(err, ErrGCPWelcomeMessage) {
		writeGCPWelcomeAccepted(w)
		return
	}
	if err != nil {
		writeUnprocessable(w, err)
		return
	}
	s.submitRequest(w, r, IntakeRoutes["gcp"], req)
}

// readPostBody enforces the POST-only, 1 MiB-bounded read every provider
// route shares. Oversize deliveries are rejected before verification so a
// hostile body cannot burn HMAC or normalization work.
func (s *Server) readPostBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method must be POST")
		return nil, false
	}
	max := s.cfg.MaxRequestBodyBytes
	if max <= 0 {
		max = DefaultMaxBodyBytes
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, max+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "cannot read delivery body")
		return nil, false
	}
	if err := checkBodyLimit(body, max); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, err.Error())
		return nil, false
	}
	noteBody(w, body)
	return body, true
}

// checkBodyLimit rejects bodies over the configured cap.
func checkBodyLimit(body []byte, max int64) error {
	if int64(len(body)) > max {
		return fmt.Errorf("delivery body exceeds %d bytes", max)
	}
	return nil
}

// submitRequest posts one normalized request to core and translates the
// outcome to the provider status: 202 accepted (exact duplicates coalesce
// to 202 in core), 409 conflicting delivery identity (permanent, matching
// the in-tree listener contract), 502 when core intake fails. GitHub does
// not retry a failed delivery on its own; an operator redelivers it by hand
// within 3 days.
func (s *Server) submitRequest(w http.ResponseWriter, r *http.Request, route string, request any) {
	receipt, err := s.submit.Submit(r.Context(), route, request)
	if err != nil {
		if err == ErrDeliveryConflict {
			writeError(w, http.StatusConflict, conflictMessage(route))
			return
		}
		writeError(w, http.StatusBadGateway, "intake submission failed")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"trigger_id": receipt.TriggerID})
}

// conflictMessage names the intake route that conflicted so a 409 on one
// route never carries another route's label.
func conflictMessage(route string) string {
	switch route {
	case IntakeRoutes["actions"]:
		return "conflicting actions delivery"
	case IntakeRoutes["incident"]:
		return "conflicting incident delivery"
	case IntakeRoutes["aws"]:
		return "conflicting aws delivery"
	case IntakeRoutes["gcp"]:
		return "conflicting gcp delivery"
	default:
		return "conflicting repository delivery"
	}
}

// deliveryID prefers the provider's delivery header and falls back to a
// content hash so deliveries without one still carry a stable idempotency
// key into the intake API.
func deliveryID(header string, body []byte) string {
	if id := strings.TrimSpace(header); id != "" {
		// Strip Bitbucket's brace-wrapped request UUIDs.
		return strings.Trim(strings.Trim(id, "{}"), `"`)
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
