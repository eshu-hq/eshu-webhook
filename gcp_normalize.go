// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrGCPWelcomeMessage reports the bare-string welcome message Cloud Asset
// Inventory sends on the first delivery to a new feed subscription. It is a
// benign signal, not a malformed event: the caller must acknowledge it with
// a 2xx (Pub/Sub retries anything else) and must not call core's intake.
var ErrGCPWelcomeMessage = errors.New("gcp freshness: pub/sub welcome message, not a temporal asset")

// GCP freshness event kinds core's intake accepts. Core derives the kind
// from the decoded asset and its deleted flag; it never reads a kind from
// Pub/Sub attributes.
const (
	gcpKindAssetChange  = "asset_change"
	gcpKindAssetDeleted = "asset_deleted"
)

// gcpPushEnvelope is the Google Cloud Pub/Sub push delivery shape.
type gcpPushEnvelope struct {
	Message struct {
		Data        string `json:"data"`
		MessageID   string `json:"messageId"`
		PublishTime string `json:"publishTime"`
	} `json:"message"`
}

// gcpTemporalAsset is the partial Cloud Asset Inventory TemporalAsset model
// the intake request needs. The resource.data blob, IAM policy, and other
// asset content are not modeled, so they cannot reach the intake request.
type gcpTemporalAsset struct {
	Asset      *gcpAsset `json:"asset"`
	PriorAsset *gcpAsset `json:"priorAsset"`
	Deleted    bool      `json:"deleted"`
	Window     struct {
		StartTime time.Time `json:"startTime"`
	} `json:"window"`
}

type gcpAsset struct {
	AssetType  string    `json:"assetType"`
	Ancestors  []string  `json:"ancestors"`
	UpdateTime time.Time `json:"updateTime"`
	Resource   struct {
		Location string `json:"location"`
	} `json:"resource"`
}

// NormalizeGCPFreshness converts a verified Cloud Asset Inventory Pub/Sub
// push delivery into the GCP intake request core's own push handler would
// build. It decodes message.data as a TemporalAsset, derives kind from the
// asset and its deleted flag, and returns ErrGCPWelcomeMessage for the
// first-subscription welcome message. It rejects what core's handler
// rejects, so core's intake never sees a request it would answer 400.
func NormalizeGCPFreshness(payload []byte) (GCPIntakeRequest, error) {
	var envelope gcpPushEnvelope
	if err := decodePayload(payload, &envelope); err != nil {
		return GCPIntakeRequest{}, err
	}
	eventID := strings.TrimSpace(envelope.Message.MessageID)
	if eventID == "" {
		return GCPIntakeRequest{}, errors.New("gcp freshness: pub/sub message is missing messageId")
	}
	data, err := base64.StdEncoding.DecodeString(envelope.Message.Data)
	if err != nil {
		return GCPIntakeRequest{}, fmt.Errorf("decode Pub/Sub push message data: %w", err)
	}
	var welcome string
	if err := json.Unmarshal(data, &welcome); err == nil {
		return GCPIntakeRequest{}, ErrGCPWelcomeMessage
	}
	var feed gcpTemporalAsset
	if err := json.Unmarshal(data, &feed); err != nil {
		return GCPIntakeRequest{}, fmt.Errorf("decode Cloud Asset Inventory TemporalAsset payload: %w", err)
	}

	kind := gcpKindAssetChange
	asset := feed.Asset
	if feed.Deleted {
		kind = gcpKindAssetDeleted
		if asset == nil {
			asset = feed.PriorAsset
		}
	}
	if asset == nil {
		return GCPIntakeRequest{}, errors.New("gcp freshness: temporal asset payload has no asset or priorAsset")
	}
	scopeKind, scopeID, err := gcpParentScope(asset.Ancestors)
	if err != nil {
		return GCPIntakeRequest{}, err
	}
	publishTime, _ := time.Parse(time.RFC3339, strings.TrimSpace(envelope.Message.PublishTime))
	observedAt := firstNonZeroTime(feed.Window.StartTime, asset.UpdateTime, publishTime)
	if observedAt.IsZero() {
		return GCPIntakeRequest{}, errors.New("gcp freshness: unable to derive observed_at from window, asset, or publish time")
	}

	req := GCPIntakeRequest{
		APIVersion:      APIVersionV1,
		EventID:         eventID,
		Kind:            kind,
		ParentScopeKind: scopeKind,
		ParentScopeID:   scopeID,
		AssetType:       strings.TrimSpace(asset.AssetType),
		Location:        strings.TrimSpace(asset.Resource.Location),
		ObservedAt:      observedAt.Format(time.RFC3339Nano),
	}
	if err := validateGCPRequest(req); err != nil {
		return GCPIntakeRequest{}, err
	}
	return req, nil
}

// validateGCPRequest applies the bounds core's trigger validation enforces
// (required asset type, no wildcards) plus the intake wire-length limits.
func validateGCPRequest(req GCPIntakeRequest) error {
	if req.AssetType == "" {
		return errors.New("asset_type is required")
	}
	for name, value := range map[string]string{
		"parent_scope_id": req.ParentScopeID,
		"asset_type":      req.AssetType,
		"location":        req.Location,
	} {
		if strings.Contains(value, "*") {
			return fmt.Errorf("%s must not contain wildcard", name)
		}
	}
	return CheckWireLengths(map[string]string{
		"event_id": req.EventID, "kind": req.Kind, "parent_scope_kind": req.ParentScopeKind,
		"parent_scope_id": req.ParentScopeID, "asset_type": req.AssetType, "location": req.Location,
	})
}

// gcpParentScope derives the parent scope kind and id from a CAI ancestors
// list such as ["projects/123", "folders/9", "organizations/1"]. The first
// parseable ancestor (the most specific) wins, as in core.
func gcpParentScope(ancestors []string) (kind, id string, err error) {
	for _, ancestor := range ancestors {
		prefix, rest, ok := strings.Cut(strings.TrimSpace(ancestor), "/")
		if !ok || strings.TrimSpace(rest) == "" {
			continue
		}
		switch prefix {
		case "projects":
			return "project", rest, nil
		case "folders":
			return "folder", rest, nil
		case "organizations":
			return "organization", rest, nil
		}
	}
	return "", "", errors.New("gcp freshness: ancestors carry no parseable parent scope")
}

// firstNonZeroTime returns the first non-zero value in UTC, or the zero time.
func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value.UTC()
		}
	}
	return time.Time{}
}
