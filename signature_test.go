// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
)

// signGitHub builds a valid X-Hub-Signature-256 value for a payload.
func signGitHub(t *testing.T, secret string, payload []byte) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	if _, err := mac.Write(payload); err != nil {
		t.Fatal(err)
	}
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyGitHubSignatureAcceptsValid(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"zen":"keep it simple"}`)
	if err := VerifyGitHubSignature(payload, "s3cret", signGitHub(t, "s3cret", payload)); err != nil {
		t.Fatalf("VerifyGitHubSignature error = %v", err)
	}
}

func TestVerifyGitHubSignatureRejectsMismatch(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"zen":"keep it simple"}`)
	if err := VerifyGitHubSignature(payload, "s3cret", signGitHub(t, "other", payload)); err == nil {
		t.Fatal("VerifyGitHubSignature = nil, want mismatch error")
	}
}

func TestVerifyGitHubSignatureRejectsLegacySHA1(t *testing.T) {
	t.Parallel()
	if err := VerifyGitHubSignature([]byte("x"), "s3cret", "sha1=deadbeef"); err == nil {
		t.Fatal("VerifyGitHubSignature accepted sha1 prefix, want rejection")
	}
}

func TestVerifyGitHubSignatureRejectsEmptySecret(t *testing.T) {
	t.Parallel()
	if err := VerifyGitHubSignature([]byte("x"), "", signGitHub(t, "s3cret", []byte("x"))); err == nil {
		t.Fatal("VerifyGitHubSignature with empty secret = nil, want error")
	}
}

func TestVerifyBitbucketSignatureRoundTrip(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"push":{}}`)
	if err := VerifyBitbucketSignature(payload, "bb", signGitHub(t, "bb", payload)); err != nil {
		t.Fatalf("VerifyBitbucketSignature error = %v", err)
	}
	if err := VerifyBitbucketSignature(payload, "bb", signGitHub(t, "bb2", payload)); err == nil {
		t.Fatal("VerifyBitbucketSignature mismatch = nil, want error")
	}
}

func TestVerifyGitLabToken(t *testing.T) {
	t.Parallel()
	if err := VerifyGitLabToken("tok", "tok"); err != nil {
		t.Fatalf("VerifyGitLabToken error = %v", err)
	}
	if err := VerifyGitLabToken("tok", "other"); err == nil {
		t.Fatal("VerifyGitLabToken mismatch = nil, want error")
	}
	if err := VerifyGitLabToken("", "tok"); err == nil {
		t.Fatal("VerifyGitLabToken empty secret = nil, want error")
	}
}

func TestVerifyPagerDutySignatureRoundTrip(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"event":"incident.trigger"}`)
	mac := hmac.New(sha256.New, []byte("pd"))
	if _, err := mac.Write(payload); err != nil {
		t.Fatal(err)
	}
	sig := "v1=" + hex.EncodeToString(mac.Sum(nil))
	if err := VerifyPagerDutySignature(payload, "pd", sig); err != nil {
		t.Fatalf("VerifyPagerDutySignature error = %v", err)
	}
	if err := VerifyPagerDutySignature(payload, "pd", "v1=00"); err == nil {
		t.Fatal("VerifyPagerDutySignature mismatch = nil, want error")
	}
}

func TestVerifyJiraSignatureRoundTrip(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"webhookEvent":"jira:issue_updated"}`)
	if err := VerifyJiraSignature(payload, "j", signGitHub(t, "j", payload)); err != nil {
		t.Fatalf("VerifyJiraSignature error = %v", err)
	}
}

func TestVerifyBearerToken(t *testing.T) {
	t.Parallel()
	if err := VerifyStaticToken("tok", "tok"); err != nil {
		t.Fatalf("VerifyStaticToken error = %v", err)
	}
	if err := VerifyStaticToken("tok", "nope"); err == nil {
		t.Fatal("VerifyStaticToken mismatch = nil, want error")
	}
	if err := VerifyStaticToken("", "tok"); err == nil {
		t.Fatal("VerifyStaticToken empty configured secret = nil, want error")
	}
}

func TestSignatureVectorsAreDistinctPerPayload(t *testing.T) {
	t.Parallel()
	a := signGitHub(t, "s", []byte(`{"a":1}`))
	b := signGitHub(t, "s", []byte(`{"a":2}`))
	if a == b {
		t.Fatal("signatures for distinct payloads must differ")
	}
	fmt.Println(a[:16], b[:16])
}
