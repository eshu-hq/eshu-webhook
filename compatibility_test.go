// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestIntakeRoutesMatchCoreContract pins the versioned intake route spellings
// the satellite may call: POST /api/v1/webhook/intake/* (#9377). A route
// drift here breaks every submission while looking healthy locally.
func TestIntakeRoutesMatchCoreContract(t *testing.T) {
	t.Parallel()

	want := map[string]string{
		"repository": "/api/v1/webhook/intake/repository",
		"actions":    "/api/v1/webhook/intake/actions",
		"incident":   "/api/v1/webhook/intake/incident",
		"aws":        "/api/v1/webhook/intake/aws",
		"gcp":        "/api/v1/webhook/intake/gcp",
	}
	if len(IntakeRoutes) != len(want) {
		t.Fatalf("len(IntakeRoutes) = %d, want %d", len(IntakeRoutes), len(want))
	}
	for family, route := range want {
		if IntakeRoutes[family] != route {
			t.Errorf("IntakeRoutes[%q] = %q, want %q", family, IntakeRoutes[family], route)
		}
	}
	if APIVersionV1 != "v1" {
		t.Fatalf("APIVersionV1 = %q, want %q", APIVersionV1, "v1")
	}
}

// TestSubmitterPresentsBearerAuth proves the submitter authenticates every
// intake call with the operator-configured Bearer [REDACTED] core
// requires (#9377).
func TestSubmitterPresentsBearerAuth(t *testing.T) {
	t.Parallel()

	var gotAuth, gotCT string
	var gotBody []byte
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		buf := make([]byte, 1<<20)
		n, _ := r.Body.Read(buf)
		gotBody = buf[:n]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"api_version":"v1","trigger_id":"t-1","status":"pending","decision":"accepted","kind":"push"}`))
	}))
	defer core.Close()

	client, err := NewIntakeClient(core.URL, "tok-abc")
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := client.Submit(context.Background(), IntakeRoutes["repository"], RepositoryIntakeRequest{
		APIVersion: APIVersionV1, Provider: "github", DeliveryID: "d1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok-abc" {
		t.Fatalf("Authorization = %q, want Bearer tok-abc", gotAuth)
	}
	if gotCT != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", gotCT)
	}
	var decoded map[string]any
	if err := json.Unmarshal(gotBody, &decoded); err != nil {
		t.Fatalf("core received invalid JSON: %v", err)
	}
	if decoded["api_version"] != "v1" {
		t.Fatalf("api_version = %v, want v1", decoded["api_version"])
	}
	if receipt.TriggerID != "t-1" {
		t.Fatalf("receipt.TriggerID = %q, want t-1", receipt.TriggerID)
	}
}

// TestSubmitterFailsClosedWithoutToken proves an empty token never builds a
// client: unauthenticated submissions must fail at construction, not as a
// silent anonymous POST.
func TestSubmitterFailsClosedWithoutToken(t *testing.T) {
	t.Parallel()

	if _, err := NewIntakeClient("http://core:8080", ""); err == nil {
		t.Fatal("NewIntakeClient(empty token) = nil, want error")
	}
	if _, err := NewIntakeClient("", "tok"); err == nil {
		t.Fatal("NewIntakeClient(empty URL) = nil, want error")
	}
	if _, err := NewIntakeClient("ftp://core/x", "tok"); err == nil {
		t.Fatal("NewIntakeClient(non-http URL) = nil, want error")
	}
}

// TestSubmitterMapsDeliveryConflict proves a 409 with the delivery-conflict
// code surfaces as ErrDeliveryConflict (dedupe, not a 500 redelivery loop).
func TestSubmitterMapsDeliveryConflict(t *testing.T) {
	t.Parallel()

	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"api_version":"v1","code":"delivery_conflict","duplicate":true}`))
	}))
	defer core.Close()

	client, err := NewIntakeClient(core.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Submit(context.Background(), IntakeRoutes["actions"], ActionsIntakeRequest{APIVersion: APIVersionV1})
	if err != ErrDeliveryConflict {
		t.Fatalf("Submit(conflict) = %v, want ErrDeliveryConflict", err)
	}
}

// TestSubmitterTreatsNon2xxAsRedeliverable proves any other non-2xx becomes
// a submission error (the handler answers 5xx so the provider redelivers).
func TestSubmitterTreatsNon2xxAsRedeliverable(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))
		client, err := NewIntakeClient(core.URL, "tok")
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.Submit(context.Background(), IntakeRoutes["incident"], IncidentIntakeRequest{APIVersion: APIVersionV1})
		if err == nil || err == ErrDeliveryConflict {
			t.Fatalf("Submit(status %d) = %v, want submission error", status, err)
		}
		core.Close()
	}
}

// TestSubmitterRequiresTriggerID proves a 200 receipt without a trigger_id
// fails closed (except the actions route, which carries no trigger id).
func TestSubmitterRequiresTriggerID(t *testing.T) {
	t.Parallel()

	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"api_version":"v1","status":"pending"}`))
	}))
	defer core.Close()

	client, err := NewIntakeClient(core.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Submit(context.Background(), IntakeRoutes["repository"], RepositoryIntakeRequest{}); err == nil {
		t.Fatal("Submit(repository, no trigger_id) = nil, want error")
	}
	if _, err := client.Submit(context.Background(), IntakeRoutes["actions"], ActionsIntakeRequest{}); err != nil {
		t.Fatalf("Submit(actions, no trigger_id) error = %v, want nil", err)
	}
}

// TestUnsupportedAPIVersionIsRejected proves the satellite stamps only the
// supported wire version: anything else fails closed on the core side, so the
// satellite must never emit it.
func TestUnsupportedAPIVersionIsRejected(t *testing.T) {
	t.Parallel()

	if err := CheckAPIVersion("v2"); err == nil {
		t.Fatal("CheckAPIVersion(v2) = nil, want error")
	}
	if err := CheckAPIVersion(""); err != nil {
		t.Fatalf("CheckAPIVersion(empty, oldest v1 client) error = %v, want nil", err)
	}
	if err := CheckAPIVersion("v1"); err != nil {
		t.Fatalf("CheckAPIVersion(v1) error = %v, want nil", err)
	}
	if strings.TrimSpace(APIVersionV1) == "" {
		t.Fatal("APIVersionV1 must be non-blank")
	}
}
