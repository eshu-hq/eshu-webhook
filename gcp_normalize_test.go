// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gcpFixtureNames are the recorded, sanitized Cloud Asset Inventory push
// deliveries under testdata/gcp. Each <name>.push.json is a Pub/Sub push
// body and each <name>.want.json is the intake request core's push handler
// produces for it (verified once against core; see README "GCP parity").
var gcpFixtureNames = []string{
	"asset_change",
	"asset_deleted",
	"folder_scope",
	"organization_scope",
	"publish_time_fallback",
}

func readGCPFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "gcp", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return body
}

// pushEnvelope wraps a decoded TemporalAsset (or any data string) in a
// Pub/Sub push body the way Pub/Sub delivers it.
func pushEnvelope(t *testing.T, messageID, data string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"message": map[string]string{
			"data":        base64.StdEncoding.EncodeToString([]byte(data)),
			"messageId":   messageID,
			"publishTime": "2026-06-01T12:00:00.000Z",
		},
		"subscription": "projects/demo-project/subscriptions/eshu-cai-feed",
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// TestNormalizeGCPFreshnessMatchesCoreGoldens proves a recorded Cloud Asset
// Inventory push becomes the same intake request core's handler would build:
// kind comes from the decoded asset and its deleted flag, scope from the
// ancestors, observed_at from window/updateTime/publishTime in core's order.
func TestNormalizeGCPFreshnessMatchesCoreGoldens(t *testing.T) {
	t.Parallel()

	for _, name := range gcpFixtureNames {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeGCPFreshness(readGCPFixture(t, name+".push.json"))
			if err != nil {
				t.Fatalf("NormalizeGCPFreshness() error = %v, want nil", err)
			}
			var want GCPIntakeRequest
			if err := json.Unmarshal(readGCPFixture(t, name+".want.json"), &want); err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("request = %+v, want %+v", got, want)
			}
		})
	}
}

// TestNormalizeGCPFreshnessWelcomeMessage proves the bare-string welcome
// message Cloud Asset Inventory sends on first subscription is a benign
// signal, not a malformed event (core: ErrWelcomeMessage).
func TestNormalizeGCPFreshnessWelcomeMessage(t *testing.T) {
	t.Parallel()

	_, err := NormalizeGCPFreshness(readGCPFixture(t, "welcome.push.json"))
	if !errors.Is(err, ErrGCPWelcomeMessage) {
		t.Fatalf("NormalizeGCPFreshness(welcome) error = %v, want ErrGCPWelcomeMessage", err)
	}
}

// TestNormalizeGCPFreshnessNeverEmitsKindCoreRejects pins the original
// defect: every accepted request carries a kind core's intake accepts.
func TestNormalizeGCPFreshnessNeverEmitsKindCoreRejects(t *testing.T) {
	t.Parallel()

	for _, name := range gcpFixtureNames {
		req, err := NormalizeGCPFreshness(readGCPFixture(t, name+".push.json"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if req.Kind != "asset_change" && req.Kind != "asset_deleted" {
			t.Errorf("%s: kind = %q, want asset_change or asset_deleted", name, req.Kind)
		}
	}
}

// TestNormalizeGCPFreshnessDeletedUsesAssetWhenPresent mirrors core: a
// deleted notification prefers asset and falls back to priorAsset.
func TestNormalizeGCPFreshnessDeletedUsesAssetWhenPresent(t *testing.T) {
	t.Parallel()

	data := `{"asset":{"assetType":"compute.googleapis.com/Disk","ancestors":["projects/7"],` +
		`"resource":{"location":"us-east1-b"}},"priorAsset":{"assetType":"compute.googleapis.com/Instance",` +
		`"ancestors":["projects/8"]},"deleted":true,"window":{"startTime":"2026-06-01T10:00:00Z"}}`
	req, err := NormalizeGCPFreshness(pushEnvelope(t, "m-1", data))
	if err != nil {
		t.Fatal(err)
	}
	if req.Kind != "asset_deleted" || req.AssetType != "compute.googleapis.com/Disk" || req.ParentScopeID != "7" {
		t.Fatalf("request = %+v, want deleted Disk in project 7", req)
	}
}

// TestNormalizeGCPFreshnessRejectsWhatCoreRejects covers the inputs core's
// push handler answers with 400; the satellite must not forward them.
func TestNormalizeGCPFreshnessRejectsWhatCoreRejects(t *testing.T) {
	t.Parallel()

	okAsset := `{"assetType":"compute.googleapis.com/Instance","ancestors":["projects/7"],` +
		`"updateTime":"2026-06-01T10:00:00Z"}`
	cases := []struct {
		name string
		body []byte
	}{
		{"not json", []byte(`not json`)},
		{"empty body", []byte(``)},
		{"attributes only, no data", []byte(`{"message":{"messageId":"m-1","attributes":{"parentScopeId":"p1","assetType":"x"}}}`)},
		{"missing message id", []byte(`{"message":{"data":"` + base64.StdEncoding.EncodeToString([]byte(`{"asset":`+okAsset+`}`)) + `"}}`)},
		{"bad base64", []byte(`{"message":{"data":"not-valid-base64!!","messageId":"m-1"}}`)},
		{"bad temporal asset json", pushEnvelope(t, "m-1", `{"asset": "not-an-object"`)},
		{"no asset", pushEnvelope(t, "m-1", `{"deleted":false}`)},
		{"prior asset without deleted flag", pushEnvelope(t, "m-1", `{"priorAsset":`+okAsset+`}`)},
		{"no ancestors", pushEnvelope(t, "m-1", `{"asset":{"assetType":"x/Y","ancestors":[],"updateTime":"2026-06-01T10:00:00Z"}}`)},
		{"unparseable ancestors", pushEnvelope(t, "m-1", `{"asset":{"assetType":"x/Y","ancestors":["billingAccounts/1"],"updateTime":"2026-06-01T10:00:00Z"}}`)},
		{"missing asset type", pushEnvelope(t, "m-1", `{"asset":{"ancestors":["projects/7"],"updateTime":"2026-06-01T10:00:00Z"}}`)},
		{"wildcard asset type", pushEnvelope(t, "m-1", `{"asset":{"assetType":"compute.googleapis.com/*","ancestors":["projects/7"],"updateTime":"2026-06-01T10:00:00Z"}}`)},
		{"wildcard parent scope", pushEnvelope(t, "m-1", `{"asset":{"assetType":"x/Y","ancestors":["projects/*"],"updateTime":"2026-06-01T10:00:00Z"}}`)},
		{"wildcard location", pushEnvelope(t, "m-1", `{"asset":{"assetType":"x/Y","ancestors":["projects/7"],"updateTime":"2026-06-01T10:00:00Z","resource":{"location":"us-*"}}}`)},
		{"no usable time", []byte(`{"message":{"messageId":"m-1","data":"` + base64.StdEncoding.EncodeToString([]byte(`{"asset":{"assetType":"x/Y","ancestors":["projects/7"]}}`)) + `"}}`)},
		{"oversize field", pushEnvelope(t, "m-1", `{"asset":{"assetType":"`+strings.Repeat("a", WireFieldLimit+1)+`","ancestors":["projects/7"],"updateTime":"2026-06-01T10:00:00Z"}}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := NormalizeGCPFreshness(tc.body)
			if err == nil {
				t.Fatal("NormalizeGCPFreshness() error = nil, want error")
			}
			if errors.Is(err, ErrGCPWelcomeMessage) {
				t.Fatalf("error = %v, must not be the welcome sentinel", err)
			}
		})
	}
}

// TestNormalizeGCPFreshnessDropsResourceData keeps the satellite as bounded
// as core: the resource.data blob never reaches the intake request.
func TestNormalizeGCPFreshnessDropsResourceData(t *testing.T) {
	t.Parallel()

	req, err := NormalizeGCPFreshness(readGCPFixture(t, "asset_change.push.json"))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"10.0.0.5", "RUNNING", "networkInterfaces"} {
		if bytes.Contains(encoded, []byte(leak)) {
			t.Fatalf("intake request leaks resource data %q: %s", leak, encoded)
		}
	}
}

// TestServerGCPWelcomeMessageIsAcknowledgedWithoutIntake proves the HTTP
// contract: a verified welcome message is answered 202 ignored, never 422
// (Pub/Sub retries non-2xx), and core is never called.
func TestServerGCPWelcomeMessageIsAcknowledgedWithoutIntake(t *testing.T) {
	t.Parallel()

	srv, stub := serveMux(t, fullTestConfig())
	req := httptest.NewRequest(http.MethodPost, "/webhook/gcp-freshness",
		bytes.NewReader(readGCPFixture(t, "welcome.push.json")))
	req.Header.Set("Authorization", "Bearer "+fullTestConfig().GCPFreshnessToken)
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("welcome status = %d, want 202 (%s)", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ignored" || body["reason"] != "welcome_message" {
		t.Fatalf("welcome body = %v, want status=ignored reason=welcome_message", body)
	}
	if len(stub.submitted) != 0 {
		t.Fatalf("welcome message made %d intake calls, want 0", len(stub.submitted))
	}
}

// TestServerGCPUnverifiedWelcomeIsStillRejected keeps auth ahead of the
// welcome short-circuit: a wrong token never gets a 2xx.
func TestServerGCPUnverifiedWelcomeIsStillRejected(t *testing.T) {
	t.Parallel()

	srv, stub := serveMux(t, fullTestConfig())
	req := httptest.NewRequest(http.MethodPost, "/webhook/gcp-freshness",
		bytes.NewReader(readGCPFixture(t, "welcome.push.json")))
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(stub.submitted) != 0 {
		t.Fatalf("unverified welcome made %d intake calls, want 0", len(stub.submitted))
	}
}

// TestServerGCPAssetChangeSubmitsCoreRequest proves the end-to-end wire: a
// recorded push reaches core's gcp intake route with the golden body.
func TestServerGCPAssetChangeSubmitsCoreRequest(t *testing.T) {
	t.Parallel()

	cfg := fullTestConfig()
	srv, stub := serveMux(t, cfg)
	req := httptest.NewRequest(http.MethodPost, "/webhook/gcp-freshness",
		bytes.NewReader(readGCPFixture(t, "asset_deleted.push.json")))
	req.Header.Set("Authorization", "Bearer "+cfg.GCPFreshnessToken)
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", rec.Code, rec.Body.String())
	}
	if len(stub.submitted) != 1 || stub.submitted[0].route != "/api/v1/webhook/intake/gcp" {
		t.Fatalf("submissions = %+v, want one gcp intake", stub.submitted)
	}
	var got, want GCPIntakeRequest
	if err := json.Unmarshal(stub.submitted[0].body, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(readGCPFixture(t, "asset_deleted.want.json"), &want); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("submitted = %+v, want %+v", got, want)
	}
}
