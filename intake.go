// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// APIVersionV1 is the only intake wire version the satellite emits.
const APIVersionV1 = "v1"

// IntakeRoutes pins the versioned core intake route spellings the satellite
// may call (#9377). A drift here breaks every submission while looking
// healthy locally, so compatibility_test.go pins each spelling.
var IntakeRoutes = map[string]string{
	"repository": "/api/v1/webhook/intake/repository",
	"actions":    "/api/v1/webhook/intake/actions",
	"incident":   "/api/v1/webhook/intake/incident",
	"aws":        "/api/v1/webhook/intake/aws",
	"gcp":        "/api/v1/webhook/intake/gcp",
}

// WireFieldLimit bounds any single intake string field. Normalized provider
// values are far smaller; the cap only stops a hostile delivery from pushing
// megabytes through one field past the body cap in many small keys.
const WireFieldLimit = 4096

// intakeRequestTimeout bounds one core intake submission. A slow core must
// surface as a 5xx to the provider (which redelivers), never as an
// unbounded handler block.
const intakeRequestTimeout = 10 * time.Second

// ErrDeliveryConflict reports a core 409 with the delivery-conflict code:
// the delivery ID is already recorded under a different identity, so the
// handler answers 409 (permanent) instead of entering a 5xx redelivery loop.
var ErrDeliveryConflict = errors.New("webhook intake delivery conflict")

// CheckAPIVersion validates the wire version without touching storage.
// Absent api_version means the oldest v1 client; any other value fails
// closed.
func CheckAPIVersion(version string) error {
	version = strings.TrimSpace(version)
	if version == "" || version == APIVersionV1 {
		return nil
	}
	return fmt.Errorf("unsupported intake api_version %q", version)
}

// CheckWireLengths bounds every intake string field so one hostile field
// cannot push megabytes past the body cap in many small keys.
func CheckWireLengths(fields map[string]string) error {
	for name, value := range fields {
		if len(value) > WireFieldLimit {
			return fmt.Errorf("intake field %q exceeds %d bytes", name, WireFieldLimit)
		}
	}
	return nil
}

// RepositoryIntakeRequest is the v1 wire form of one normalized repository
// trigger (github, gitlab, or bitbucket) submitted after provider signature
// verification. Field spellings match the core intake contract.
type RepositoryIntakeRequest struct {
	APIVersion           string `json:"api_version"`
	Provider             string `json:"provider"`
	DeliveryID           string `json:"delivery_id"`
	EventKind            string `json:"event_kind"`
	Decision             string `json:"decision"`
	Reason               string `json:"reason"`
	RepositoryExternalID string `json:"repository_external_id"`
	RepositoryFullName   string `json:"repository_full_name"`
	DefaultBranch        string `json:"default_branch"`
	Ref                  string `json:"ref"`
	BeforeSHA            string `json:"before_sha"`
	TargetSHA            string `json:"target_sha"`
	Action               string `json:"action"`
	Sender               string `json:"sender"`
	PullRequestNumber    string `json:"pull_request_number"`
	PullRequestURL       string `json:"pull_request_url"`
	PullRequestTitle     string `json:"pull_request_title"`
}

// ActionsIntakeRequest is the v1 wire form of one verified GitHub Actions
// wake-up identity.
type ActionsIntakeRequest struct {
	APIVersion   string `json:"api_version"`
	Provider     string `json:"provider"`
	DeliveryID   string `json:"delivery_id"`
	EventKind    string `json:"event_kind"`
	Action       string `json:"action"`
	RepositoryID string `json:"repository_id"`
	Repository   string `json:"repository"`
	RunID        string `json:"run_id"`
	JobID        string `json:"job_id"`
}

// IncidentIntakeRequest is the v1 wire form of one normalized incident
// freshness trigger (pagerduty or jira).
type IncidentIntakeRequest struct {
	APIVersion string `json:"api_version"`
	Provider   string `json:"provider"`
	EventKind  string `json:"event_kind"`
	EventID    string `json:"event_id"`
	ScopeID    string `json:"scope_id"`
	ResourceID string `json:"resource_id"`
	ObservedAt string `json:"observed_at"`
}

// AWSIntakeRequest is the v1 wire form of one normalized AWS freshness trigger.
type AWSIntakeRequest struct {
	APIVersion   string `json:"api_version"`
	EventID      string `json:"event_id"`
	Kind         string `json:"kind"`
	AccountID    string `json:"account_id"`
	Region       string `json:"region"`
	ServiceKind  string `json:"service_kind"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	ObservedAt   string `json:"observed_at"`
}

// GCPIntakeRequest is the v1 wire form of one normalized GCP freshness trigger.
type GCPIntakeRequest struct {
	APIVersion      string `json:"api_version"`
	EventID         string `json:"event_id"`
	Kind            string `json:"kind"`
	ParentScopeKind string `json:"parent_scope_kind"`
	ParentScopeID   string `json:"parent_scope_id"`
	AssetType       string `json:"asset_type"`
	Location        string `json:"location"`
	ObservedAt      string `json:"observed_at"`
}

// Receipt is the durable receipt the core intake API returns for one
// accepted submission.
type Receipt struct {
	APIVersion     string `json:"api_version"`
	TriggerID      string `json:"trigger_id"`
	Status         string `json:"status"`
	Decision       string `json:"decision"`
	Reason         string `json:"reason"`
	Kind           string `json:"kind"`
	Duplicate      bool   `json:"duplicate"`
	DuplicateCount int    `json:"duplicate_count"`
	Code           string `json:"code"`
}

// DuplicateCount normalizes the receipt's duplicate signal: a duplicate
// submission reports at least 1 so handler duplicate telemetry stays exact.
func DuplicateCount(receipt Receipt) int {
	if !receipt.Duplicate {
		return 0
	}
	return max(receipt.DuplicateCount, 1)
}

// IntakeClient posts normalized triggers to core's authenticated versioned
// intake API (#9377). It performs zero SQL: on the intake path the satellite
// is a verifier/normalizer only, and core retains durable trigger storage
// and lifecycle.
type IntakeClient struct {
	baseURL string
	token   string
	client  *http.Client
}

// NewIntakeClient builds a submitter for the core intake API base URL
// (scheme + host, no route suffix). An empty URL or token fails closed: the
// satellite must never silently fall back to direct table writes.
func NewIntakeClient(baseURL, token string) (*IntakeClient, error) {
	return newIntakeClientWithHTTP(baseURL, token, nil)
}

func newIntakeClientWithHTTP(baseURL, token string, client *http.Client) (*IntakeClient, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, errors.New("webhook intake URL is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("webhook intake URL must be an http(s) base URL: %q", baseURL)
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("webhook intake token is required")
	}
	if client == nil {
		client = &http.Client{Timeout: intakeRequestTimeout}
	}
	return &IntakeClient{baseURL: baseURL, token: token, client: client}, nil
}

// Submit posts one wire request and decodes the receipt. A 409 with the
// delivery-conflict code becomes ErrDeliveryConflict, and everything else
// non-2xx is a submission error (the handler answers 5xx so the provider
// redelivers).
func (c *IntakeClient) Submit(ctx context.Context, route string, request any) (Receipt, error) {
	var receipt Receipt
	body, err := json.Marshal(request)
	if err != nil {
		return receipt, fmt.Errorf("encode intake submission: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+route, bytes.NewReader(body))
	if err != nil {
		return receipt, fmt.Errorf("build intake submission: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+c.token)
	// #nosec G107, G704 -- the request URL is the operator-configured
	// intake base URL (validated to http(s) scheme plus host at
	// construction) joined with a pinned route constant; no delivery
	// content reaches the URL, so the taint path is not exploitable.
	response, err := c.client.Do(httpRequest)
	if err != nil {
		return receipt, fmt.Errorf("submit intake %s: %w", route, err)
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return receipt, fmt.Errorf("read intake %s response: %w", route, err)
	}
	if response.StatusCode == http.StatusConflict {
		var conflict Receipt
		if err := json.Unmarshal(responseBody, &conflict); err == nil && conflict.Code == "delivery_conflict" {
			return receipt, ErrDeliveryConflict
		}
		return receipt, fmt.Errorf("submit intake %s: unexpected status %d", route, response.StatusCode)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return receipt, fmt.Errorf("submit intake %s: unexpected status %d", route, response.StatusCode)
	}
	if err := json.Unmarshal(responseBody, &receipt); err != nil {
		return receipt, fmt.Errorf("decode intake %s receipt: %w", route, err)
	}
	if receipt.TriggerID == "" && route != IntakeRoutes["actions"] {
		return receipt, fmt.Errorf("decode intake %s receipt: missing trigger_id", route)
	}
	return receipt, nil
}
