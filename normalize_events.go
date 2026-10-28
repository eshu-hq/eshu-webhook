// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// NormalizeGitHubActions converts a verified workflow_run or workflow_job
// payload into an actions intake request.
func NormalizeGitHubActions(event string, deliveryID string, payload []byte) (ActionsIntakeRequest, error) {
	event = strings.TrimSpace(event)
	if event != "workflow_run" && event != "workflow_job" {
		return ActionsIntakeRequest{}, fmt.Errorf("unsupported github actions event %q", event)
	}
	var decoded struct {
		Action     string `json:"action"`
		Repository struct {
			ID       json.RawMessage `json:"id"`
			FullName string          `json:"full_name"`
		} `json:"repository"`
		WorkflowRun struct {
			ID json.RawMessage `json:"id"`
		} `json:"workflow_run"`
		WorkflowJob struct {
			ID    json.RawMessage `json:"id"`
			RunID json.RawMessage `json:"run_id"`
		} `json:"workflow_job"`
		RunID json.RawMessage `json:"run_id"`
	}
	if err := decodePayload(payload, &decoded); err != nil {
		return ActionsIntakeRequest{}, err
	}
	runID := rawScalar(decoded.WorkflowRun.ID)
	if runID == "" {
		runID = rawScalar(decoded.RunID)
	}
	if runID == "" {
		runID = rawScalar(decoded.WorkflowJob.RunID)
	}
	req := ActionsIntakeRequest{
		APIVersion:   APIVersionV1,
		Provider:     "github",
		DeliveryID:   strings.TrimSpace(deliveryID),
		EventKind:    event,
		Action:       strings.TrimSpace(decoded.Action),
		RepositoryID: rawScalar(decoded.Repository.ID),
		Repository:   strings.ToLower(strings.Trim(strings.TrimSpace(decoded.Repository.FullName), "/")),
		RunID:        runID,
		JobID:        rawScalar(decoded.WorkflowJob.ID),
	}
	if req.DeliveryID == "" || req.Action == "" || req.RepositoryID == "" || req.RunID == "" {
		return ActionsIntakeRequest{}, errors.New("github actions identity is missing required fields")
	}
	if event == "workflow_job" && req.JobID == "" {
		return ActionsIntakeRequest{}, errors.New("github workflow_job identity requires job id")
	}
	if err := CheckWireLengths(map[string]string{
		"delivery_id": req.DeliveryID, "action": req.Action, "repository_id": req.RepositoryID,
		"repository": req.Repository, "run_id": req.RunID, "job_id": req.JobID,
	}); err != nil {
		return ActionsIntakeRequest{}, err
	}
	return req, nil
}

// NormalizePagerDuty converts a verified PagerDuty webhook into an incident
// intake request scoped to the operator-configured scope.
func NormalizePagerDuty(payload []byte, scopeID string) (IncidentIntakeRequest, error) {
	var event struct {
		Event struct {
			ID   string `json:"id"`
			Type string `json:"event_type"`
		} `json:"event"`
		Incident struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"incident"`
	}
	if err := decodePayload(payload, &event); err != nil {
		return IncidentIntakeRequest{}, err
	}
	return newIncidentRequest("pagerduty", event.Event.Type, firstNonEmpty(event.Event.ID, event.Incident.ID),
		scopeID, event.Incident.ID)
}

// NormalizeJira converts a verified Jira webhook into an incident intake
// request scoped to the operator-configured scope.
func NormalizeJira(payload []byte, scopeID string) (IncidentIntakeRequest, error) {
	var event struct {
		WebhookEvent string `json:"webhookEvent"`
		IssueEventID int64  `json:"issueEventId"`
		Issue        struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		} `json:"issue"`
	}
	if err := decodePayload(payload, &event); err != nil {
		return IncidentIntakeRequest{}, err
	}
	eventID := strings.TrimSpace(event.Issue.Key)
	if event.IssueEventID > 0 {
		eventID = fmt.Sprintf("%s:%d", eventID, event.IssueEventID)
	}
	return newIncidentRequest("jira", event.WebhookEvent, eventID, scopeID, event.Issue.ID)
}

// NormalizeAWSFreshness converts a verified EventBridge delivery (bearer
// token auth) into an AWS intake request.
func NormalizeAWSFreshness(payload []byte) (AWSIntakeRequest, error) {
	var event struct {
		ID         string `json:"id"`
		DetailType string `json:"detail-type"`
		Account    string `json:"account"`
		Region     string `json:"region"`
		Source     string `json:"source"`
		Time       string `json:"time"`
		Detail     struct {
			ResourceType string `json:"resourceType"`
			ResourceID   string `json:"resourceId"`
		} `json:"detail"`
		Resources []string `json:"resources"`
	}
	if err := decodePayload(payload, &event); err != nil {
		return AWSIntakeRequest{}, err
	}
	resourceID := strings.TrimSpace(event.Detail.ResourceID)
	if resourceID == "" && len(event.Resources) > 0 {
		resourceID = strings.TrimSpace(event.Resources[0])
	}
	req := AWSIntakeRequest{
		APIVersion:   APIVersionV1,
		EventID:      strings.TrimSpace(event.ID),
		Kind:         strings.TrimSpace(event.DetailType),
		AccountID:    strings.TrimSpace(event.Account),
		Region:       strings.TrimSpace(event.Region),
		ServiceKind:  strings.TrimSpace(event.Source),
		ResourceType: strings.TrimSpace(event.Detail.ResourceType),
		ResourceID:   resourceID,
		ObservedAt:   parseObservedAt(event.Time),
	}
	if req.EventID == "" || req.Kind == "" || req.AccountID == "" || req.Region == "" {
		return AWSIntakeRequest{}, errors.New("aws freshness event is missing required fields")
	}
	if err := CheckWireLengths(map[string]string{
		"event_id": req.EventID, "kind": req.Kind, "account_id": req.AccountID,
		"region": req.Region, "service_kind": req.ServiceKind,
		"resource_type": req.ResourceType, "resource_id": req.ResourceID,
	}); err != nil {
		return AWSIntakeRequest{}, err
	}
	return req, nil
}

// NormalizeGCPFreshness converts a verified Pub/Sub push delivery (bearer
// token auth) into a GCP intake request.
func NormalizeGCPFreshness(payload []byte) (GCPIntakeRequest, error) {
	var event struct {
		Message struct {
			MessageID  string `json:"messageId"`
			Attributes struct {
				AssetType       string `json:"assetType"`
				Location        string `json:"location"`
				ParentScopeKind string `json:"parentScopeKind"`
				ParentScopeID   string `json:"parentScopeId"`
				Kind            string `json:"kind"`
			} `json:"attributes"`
			PublishTime string `json:"publishTime"`
		} `json:"message"`
	}
	if err := decodePayload(payload, &event); err != nil {
		return GCPIntakeRequest{}, err
	}
	attrs := event.Message.Attributes
	req := GCPIntakeRequest{
		APIVersion:      APIVersionV1,
		EventID:         strings.TrimSpace(event.Message.MessageID),
		Kind:            firstNonEmpty(strings.TrimSpace(attrs.Kind), "asset_update"),
		ParentScopeKind: strings.TrimSpace(attrs.ParentScopeKind),
		ParentScopeID:   strings.TrimSpace(attrs.ParentScopeID),
		AssetType:       strings.TrimSpace(attrs.AssetType),
		Location:        strings.TrimSpace(attrs.Location),
		ObservedAt:      parseObservedAt(event.Message.PublishTime),
	}
	if req.EventID == "" || req.ParentScopeID == "" {
		return GCPIntakeRequest{}, errors.New("gcp freshness push is missing required fields")
	}
	if err := CheckWireLengths(map[string]string{
		"event_id": req.EventID, "kind": req.Kind, "parent_scope_kind": req.ParentScopeKind,
		"parent_scope_id": req.ParentScopeID, "asset_type": req.AssetType, "location": req.Location,
	}); err != nil {
		return GCPIntakeRequest{}, err
	}
	return req, nil
}

func newIncidentRequest(provider, eventKind, eventID, scopeID, resourceID string) (IncidentIntakeRequest, error) {
	req := IncidentIntakeRequest{
		APIVersion: APIVersionV1,
		Provider:   provider,
		EventKind:  strings.TrimSpace(eventKind),
		EventID:    strings.TrimSpace(eventID),
		ScopeID:    strings.TrimSpace(scopeID),
		ResourceID: strings.TrimSpace(resourceID),
		ObservedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if req.EventKind == "" || req.EventID == "" || req.ScopeID == "" {
		return IncidentIntakeRequest{}, errors.New("incident trigger requires event_kind, event_id, and scope_id")
	}
	if err := CheckWireLengths(map[string]string{
		"event_kind": req.EventKind, "event_id": req.EventID,
		"scope_id": req.ScopeID, "resource_id": req.ResourceID,
	}); err != nil {
		return IncidentIntakeRequest{}, err
	}
	return req, nil
}

func parseObservedAt(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Now().UTC().Format(time.RFC3339)
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed.UTC().Format(time.RFC3339)
	}
	return time.Now().UTC().Format(time.RFC3339)
}
