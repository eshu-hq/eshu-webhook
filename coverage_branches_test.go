// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import (
	"strings"
	"testing"
)

const zeroSHA = "0000000000000000000000000000000000000000"

// TestCoverageDecodeAndIdentityErrors proves every repository normalizer
// fails closed on undecodable payloads and on payloads missing the identity
// fields newRepositoryRequest requires.
func TestCoverageDecodeAndIdentityErrors(t *testing.T) {
	t.Parallel()

	garbage := []byte(`{"x":`)
	empty := []byte(`   `)
	bare := []byte(`{"ref":"refs/heads/main"}`)
	cases := []struct {
		name string
		call func() error
	}{
		{"github-push-garbage", func() error { _, err := NormalizeGitHub("push", "d", garbage, "main"); return err }},
		{"github-push-empty", func() error { _, err := NormalizeGitHub("push", "d", empty, "main"); return err }},
		{"github-push-bare", func() error { _, err := NormalizeGitHub("push", "d", bare, "main"); return err }},
		{"github-unsupported", func() error { _, err := NormalizeGitHub("ping", "d", bare, "main"); return err }},
		{"github-actions-route", func() error { _, err := NormalizeGitHub("workflow_run", "d", bare, "main"); return err }},
		{"github-pr-garbage", func() error { _, err := NormalizeGitHub("pull_request", "d", garbage, "main"); return err }},
		{"gitlab-push-garbage", func() error { _, err := NormalizeGitLab("Push Hook", "d", garbage, "main"); return err }},
		{"gitlab-push-bare", func() error { _, err := NormalizeGitLab("Push Hook", "d", bare, "main"); return err }},
		{"gitlab-unsupported", func() error {
			_, err := NormalizeGitLab("Push Hook", "d", []byte(`{"object_kind":"wiki"}`), "main")
			return err
		}},
		{"bitbucket-push-garbage", func() error { _, err := NormalizeBitbucket("repo:push", "d", garbage, "main"); return err }},
		{"bitbucket-push-bare", func() error { _, err := NormalizeBitbucket("repo:push", "d", bare, "main"); return err }},
		{"bitbucket-unsupported", func() error { _, err := NormalizeBitbucket("repo:fork", "d", bare, "main"); return err }},
		{"actions-unsupported", func() error { _, err := NormalizeGitHubActions("push", "d", bare); return err }},
		{"actions-missing", func() error { _, err := NormalizeGitHubActions("workflow_run", "d", bare); return err }},
		{"actions-job-missing", func() error {
			_, err := NormalizeGitHubActions("workflow_job", "d", []byte(`{"action":"queued","repository":{"id":1,"full_name":"o/r"},"run_id":9}`))
			return err
		}},
		{"pagerduty-garbage", func() error { _, err := NormalizePagerDuty(garbage, "scope"); return err }},
		{"pagerduty-missing", func() error { _, err := NormalizePagerDuty([]byte(`{}`), "scope"); return err }},
		{"jira-garbage", func() error { _, err := NormalizeJira(garbage, "scope"); return err }},
		{"jira-missing", func() error { _, err := NormalizeJira([]byte(`{}`), "scope"); return err }},
		{"aws-garbage", func() error { _, err := NormalizeAWSFreshness(garbage); return err }},
		{"aws-missing", func() error { _, err := NormalizeAWSFreshness([]byte(`{}`)); return err }},
		{"gcp-garbage", func() error { _, err := NormalizeGCPFreshness(garbage); return err }},
		{"gcp-missing", func() error { _, err := NormalizeGCPFreshness([]byte(`{}`)); return err }},
	}
	for _, tc := range cases {
		if err := tc.call(); err == nil {
			t.Errorf("%s = nil, want error", tc.name)
		}
	}
}

// TestCoverageBranchDecisions proves the branch decision matrix: tag and
// off-branch pushes are ignored with their reasons, branch deletions are
// distinguished from missing commits, and unmerged or SHA-less merge
// requests fail closed before submission.
func TestCoverageBranchDecisions(t *testing.T) {
	t.Parallel()

	pushPayload := func(ref, after string) []byte {
		return []byte(`{"ref":` + strconvQuote(ref) + `,"before":"a0","after":` + strconvQuote(after) + `,` +
			`"repository":{"id":7,"full_name":"o/r","default_branch":"main"},"sender":{"login":"u"}}`)
	}
	cases := []struct {
		name   string
		req    RepositoryIntakeRequest
		err    bool
		reason string
	}{
		{"tag", mustNormalize(t, "tag", pushPayload("refs/tags/v1", "b1")), false, ReasonTagRef},
		{"off-branch", mustNormalize(t, "off-branch", pushPayload("refs/heads/feature", "b1")), false, ReasonNonDefaultBranch},
		{"deleted", mustNormalize(t, "deleted", pushPayload("refs/heads/main", zeroSHA)), false, ReasonDeletedBranch},
		{"missing-sha", mustNormalize(t, "missing-sha", pushPayload("refs/heads/main", "")), false, ReasonMissingMergeCommit},
	}
	for _, tc := range cases {
		if tc.req.Decision != DecisionIgnored || tc.req.Reason != tc.reason {
			t.Errorf("%s: decision=%q reason=%q, want ignored/%q", tc.name, tc.req.Decision, tc.req.Reason, tc.reason)
		}
	}

	prPayload := func(action string, merged bool, sha string) []byte {
		return []byte(`{"action":` + strconvQuote(action) + `,"number":3,` +
			`"repository":{"id":7,"full_name":"o/r","default_branch":"main"},` +
			`"pull_request":{"merged":` + boolStr(merged) + `,"base":{"ref":"main"},` +
			`"merge_commit_sha":` + strconvQuote(sha) + `,"html_url":"u","title":"t"}}`)
	}
	req, err := NormalizeGitHub("pull_request", "d", prPayload("opened", false, ""), "main")
	if err != nil {
		t.Fatal(err)
	}
	if req.Decision != DecisionIgnored || req.Reason != ReasonPullRequestNotMerged {
		t.Fatalf("unmerged PR: decision=%q reason=%q", req.Decision, req.Reason)
	}
	req, err = NormalizeGitHub("pull_request", "d", prPayload("closed", true, ""), "main")
	if err != nil {
		t.Fatal(err)
	}
	if req.Decision != DecisionIgnored || req.Reason != ReasonMissingMergeCommit {
		t.Fatalf("SHA-less PR: decision=%q reason=%q", req.Decision, req.Reason)
	}

	mrPayload := func(state, sha string) []byte {
		return []byte(`{"object_kind":"merge_request",` +
			`"project":{"id":7,"path_with_namespace":"g/p","default_branch":"main"},` +
			`"object_attributes":{"action":"open","state":` + strconvQuote(state) +
			`,"merge_commit_sha":` + strconvQuote(sha) + `,"target_branch":"main","iid":4},` +
			`"user":{"username":"u"}}`)
	}
	req, err = NormalizeGitLab("Merge Request Hook", "d", mrPayload("opened", ""), "main")
	if err != nil {
		t.Fatal(err)
	}
	if req.Decision != DecisionIgnored || req.Reason != ReasonPullRequestNotMerged {
		t.Fatalf("unmerged MR: decision=%q reason=%q", req.Decision, req.Reason)
	}
	req, err = NormalizeGitLab("Merge Request Hook", "d", mrPayload("merged", ""), "main")
	if err != nil {
		t.Fatal(err)
	}
	if req.Decision != DecisionIgnored || req.Reason != ReasonMissingMergeCommit {
		t.Fatalf("SHA-less MR: decision=%q reason=%q", req.Decision, req.Reason)
	}

	bbPR := []byte(`{"pullrequest":{"id":9,"title":"t",` +
		`"destination":{"branch":{"name":"main"},"commit":{"hash":""}},` +
		`"author":{"nickname":"u"}},` +
		`"repository":{"uuid":"{u}","full_name":"t/r","mainbranch":{"name":"main"}}}`)
	req, err = NormalizeBitbucket("pullrequest:fulfilled", "d", bbPR, "main")
	if err != nil {
		t.Fatal(err)
	}
	if req.Decision != DecisionIgnored || req.Reason != ReasonMissingMergeCommit {
		t.Fatalf("SHA-less Bitbucket PR: decision=%q reason=%q", req.Decision, req.Reason)
	}
}

func mustNormalize(t *testing.T, name string, payload []byte) RepositoryIntakeRequest {
	t.Helper()
	req, err := NormalizeGitHub("push", "d-"+name, payload, "main")
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return req
}

func strconvQuote(s string) string {
	if s == "" {
		return `""`
	}
	return `"` + s + `"`
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// TestCoverageRepositoryIdentityEdges proves the construction gate: a
// delivery without any usable default branch fails, and an oversized
// identity field fails before any branch decision.
func TestCoverageRepositoryIdentityEdges(t *testing.T) {
	t.Parallel()

	bare := []byte(`{"ref":"refs/heads/main","before":"a","after":"b",` +
		`"repository":{"id":7,"full_name":"o/r"},"sender":{"login":"u"}}`)
	if _, err := NormalizeGitHub("push", "d", bare, ""); err == nil {
		t.Error("NormalizeGitHub(no branch anywhere) = nil, want identity error")
	}
	big := []byte(`{"ref":"refs/heads/main","before":"a","after":"b",` +
		`"repository":{"id":7,"full_name":` + strconvQuote(strings.Repeat("n", WireFieldLimit+1)) +
		`,"default_branch":"main"},"sender":{"login":"u"}}`)
	if _, err := NormalizeGitHub("push", "d", big, "main"); err == nil {
		t.Error("NormalizeGitHub(oversize identity) = nil, want wire-length error")
	}
}

// TestCoverageAWSResourceFallback proves the EventBridge resource fallback:
// an empty detail.resourceId falls back to the first entry of resources.
func TestCoverageAWSResourceFallback(t *testing.T) {
	t.Parallel()

	req, err := NormalizeAWSFreshness([]byte(`{"id":"e","detail-type":"k","account":"a",` +
		`"region":"r","source":"s","detail":{},"resources":["arn:1"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if req.ResourceID != "arn:1" {
		t.Fatalf("ResourceID = %q, want arn:1 fallback", req.ResourceID)
	}
}

// TestCoverageSignatureEdges proves the verifier edge branches: empty
// secrets fail closed, PagerDuty candidates without a v1 prefix or with bad
// hex are skipped, and a missing sha256 prefix is rejected before compare.
func TestCoverageSignatureEdges(t *testing.T) {
	t.Parallel()

	if err := VerifyPagerDutySignature([]byte("p"), "", "v1=00"); err == nil {
		t.Error("PagerDuty(empty secret) = nil, want error")
	}
	if err := VerifyPagerDutySignature([]byte("p"), "s", "bogus"); err == nil {
		t.Error("PagerDuty(no v1 candidate) = nil, want mismatch")
	}
	if err := VerifyPagerDutySignature([]byte("p"), "s", "v1=zz"); err == nil {
		t.Error("PagerDuty(bad hex) = nil, want mismatch")
	}
	if err := VerifyGitHubSignature([]byte("p"), "s", "bogus"); err == nil {
		t.Error("GitHub(missing prefix) = nil, want error")
	}
	if err := VerifyGitLabToken("s", ""); err == nil {
		t.Error("GitLab(empty token) = nil, want error")
	}
	if err := VerifyStaticToken("", "t"); err == nil {
		t.Error("Static(empty secret) = nil, want error")
	}
	if err := VerifyStaticToken("s", ""); err == nil {
		t.Error("Static(empty token) = nil, want error")
	}
}

// TestCoverageFirstNonEmpty pins the fallback helper, including the
// all-empty case.
func TestCoverageFirstNonEmpty(t *testing.T) {
	t.Parallel()

	if got := firstNonEmpty("", "b", "c"); got != "b" {
		t.Fatalf("firstNonEmpty = %q, want b", got)
	}
	if got := firstNonEmpty(); got != "" {
		t.Fatalf("firstNonEmpty() = %q, want empty", got)
	}
}
