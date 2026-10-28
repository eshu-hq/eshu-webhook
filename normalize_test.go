// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import (
	"strings"
	"testing"
)

func TestNormalizeGitHubPushAcceptedOnDefaultBranch(t *testing.T) {
	t.Parallel()

	req, err := NormalizeGitHub("push", "d1", []byte(githubPushPayload), "main")
	if err != nil {
		t.Fatal(err)
	}
	if req.Decision != DecisionAccepted {
		t.Fatalf("Decision = %q, want accepted", req.Decision)
	}
	if req.EventKind != "push" || req.Provider != "github" {
		t.Fatalf("route keys = %q/%q, want github/push", req.Provider, req.EventKind)
	}
	if req.RepositoryExternalID != "42" || req.RepositoryFullName != "octo/repo" || req.DefaultBranch != "main" {
		t.Fatalf("identity = %+v, want 42/octo/repo/main", req)
	}
	if req.TargetSHA != "def111" || req.Ref != "refs/heads/main" || req.Sender != "octo" {
		t.Fatalf("push fields = %+v", req)
	}
}

func TestNormalizeGitHubPushIgnoredOffDefaultBranch(t *testing.T) {
	t.Parallel()

	payload := strings.Replace(githubPushPayload, "refs/heads/main", "refs/heads/feature", 1)
	req, err := NormalizeGitHub("push", "d1", []byte(payload), "main")
	if err != nil {
		t.Fatal(err)
	}
	if req.Decision != DecisionIgnored || req.Reason != ReasonNonDefaultBranch {
		t.Fatalf("decision/reason = %q/%q, want ignored/non_default_branch", req.Decision, req.Reason)
	}
}

func TestNormalizeGitHubPushIgnoredForTagAndDeletion(t *testing.T) {
	t.Parallel()

	tag := strings.Replace(githubPushPayload, "refs/heads/main", "refs/tags/v1", 1)
	req, err := NormalizeGitHub("push", "d1", []byte(tag), "main")
	if err != nil {
		t.Fatal(err)
	}
	if req.Decision != DecisionIgnored || req.Reason != ReasonTagRef {
		t.Fatalf("tag decision/reason = %q/%q", req.Decision, req.Reason)
	}

	deleted := strings.Replace(githubPushPayload, `"after":"def111"`, `"after":"0000000"`, 1)
	req, err = NormalizeGitHub("push", "d1", []byte(deleted), "main")
	if err != nil {
		t.Fatal(err)
	}
	if req.Decision != DecisionIgnored || req.Reason != ReasonDeletedBranch {
		t.Fatalf("deletion decision/reason = %q/%q", req.Decision, req.Reason)
	}
}

func TestNormalizeGitHubPullRequestMergedAccepted(t *testing.T) {
	t.Parallel()

	payload := `{"action":"closed","number":9,` +
		`"repository":{"id":42,"full_name":"octo/repo","default_branch":"main"},` +
		`"pull_request":{"merged":true,"merge_commit_sha":"m1","base":{"ref":"main"},` +
		`"html_url":"https://example/x/9","title":"T"}}`
	req, err := NormalizeGitHub("pull_request", "d2", []byte(payload), "")
	if err != nil {
		t.Fatal(err)
	}
	if req.Decision != DecisionAccepted || req.EventKind != "pull_request_merged" {
		t.Fatalf("decision/kind = %q/%q", req.Decision, req.EventKind)
	}
	if req.PullRequestNumber != "9" || req.TargetSHA != "m1" || req.Ref != "refs/heads/main" {
		t.Fatalf("PR fields = %+v", req)
	}
}

func TestNormalizeGitHubPullRequestOpenedIgnored(t *testing.T) {
	t.Parallel()

	payload := `{"action":"opened","number":9,` +
		`"repository":{"id":42,"full_name":"octo/repo","default_branch":"main"},` +
		`"pull_request":{"merged":false,"base":{"ref":"main"}}}`
	req, err := NormalizeGitHub("pull_request", "d2", []byte(payload), "")
	if err != nil {
		t.Fatal(err)
	}
	if req.Decision != DecisionIgnored || req.Reason != ReasonPullRequestNotMerged {
		t.Fatalf("decision/reason = %q/%q", req.Decision, req.Reason)
	}
}

func TestNormalizeGitHubMergedWithoutSHIAIgnored(t *testing.T) {
	t.Parallel()

	payload := `{"action":"closed",` +
		`"repository":{"id":42,"full_name":"octo/repo","default_branch":"main"},` +
		`"pull_request":{"merged":true,"base":{"ref":"main"}}}`
	req, err := NormalizeGitHub("pull_request", "d2", []byte(payload), "")
	if err != nil {
		t.Fatal(err)
	}
	if req.Decision != DecisionIgnored || req.Reason != ReasonMissingMergeCommit {
		t.Fatalf("decision/reason = %q/%q", req.Decision, req.Reason)
	}
}

func TestNormalizeGitHubRejectsUnknownEvent(t *testing.T) {
	t.Parallel()

	if _, err := NormalizeGitHub("issues", "d1", []byte(`{}`), "main"); err == nil {
		t.Fatal("NormalizeGitHub(issues) = nil, want error")
	}
	if _, err := NormalizeGitHub("workflow_run", "d1", []byte(`{}`), "main"); err == nil {
		t.Fatal("NormalizeGitHub(workflow_run via repository path) = nil, want redirect error")
	}
}

func TestNormalizeGitHubActionsWorkflowRun(t *testing.T) {
	t.Parallel()

	payload := `{"action":"completed",` +
		`"repository":{"id":42,"full_name":"Octo/Repo"},` +
		`"workflow_run":{"id":100}}`
	req, err := NormalizeGitHubActions("workflow_run", "d3", []byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	if req.RunID != "100" || req.RepositoryID != "42" || req.Repository != "octo/repo" {
		t.Fatalf("actions identity = %+v", req)
	}

	job := `{"action":"completed",` +
		`"repository":{"id":42,"full_name":"octo/repo"},` +
		`"workflow_job":{"id":7,"run_id":100}}`
	req, err = NormalizeGitHubActions("workflow_job", "d4", []byte(job))
	if err != nil {
		t.Fatal(err)
	}
	if req.JobID != "7" || req.RunID != "100" {
		t.Fatalf("job identity = %+v", req)
	}
}

func TestNormalizeGitHubActionsRejectsIncomplete(t *testing.T) {
	t.Parallel()

	if _, err := NormalizeGitHubActions("push", "d", []byte(`{}`)); err == nil {
		t.Fatal("NormalizeGitHubActions(push) = nil, want error")
	}
	if _, err := NormalizeGitHubActions("workflow_run", "d", []byte(`{"action":"completed"}`)); err == nil {
		t.Fatal("NormalizeGitHubActions(missing ids) = nil, want error")
	}
	if _, err := NormalizeGitHubActions("workflow_job", "d", []byte(
		`{"action":"x","repository":{"id":1,"full_name":"o/r"},"workflow_job":{"run_id":5}}`)); err == nil {
		t.Fatal("NormalizeGitHubActions(job without job id) = nil, want error")
	}
}

func TestNormalizeGitLabPushAndMergeRequest(t *testing.T) {
	t.Parallel()

	push := `{"object_kind":"push","ref":"refs/heads/main","before":"a0","after":"b1",` +
		`"project":{"id":7,"path_with_namespace":"grp/proj","default_branch":"main"},` +
		`"user_username":"u","checkout_sha":"b1"}`
	req, err := NormalizeGitLab("Push Hook", "d5", []byte(push), "")
	if err != nil {
		t.Fatal(err)
	}
	if req.Decision != DecisionAccepted || req.Provider != "gitlab" || req.TargetSHA != "b1" {
		t.Fatalf("gitlab push = %+v", req)
	}

	mr := `{"object_kind":"merge_request",` +
		`"project":{"id":7,"path_with_namespace":"grp/proj","default_branch":"main"},` +
		`"object_attributes":{"action":"merge","state":"merged","merge_commit_sha":"m2",` +
		`"target_branch":"main","iid":4,"title":"T","url":"https://example/mr/4"},` +
		`"user":{"username":"u"}}`
	req, err = NormalizeGitLab("Merge Request Hook", "d6", []byte(mr), "")
	if err != nil {
		t.Fatal(err)
	}
	if req.Decision != DecisionAccepted || req.EventKind != "merge_request_merged" || req.PullRequestNumber != "4" {
		t.Fatalf("gitlab MR = %+v", req)
	}

	open := strings.Replace(mr, `"state":"merged"`, `"state":"opened"`, 1)
	req, err = NormalizeGitLab("Merge Request Hook", "d7", []byte(open), "")
	if err != nil {
		t.Fatal(err)
	}
	if req.Decision != DecisionIgnored || req.Reason != ReasonPullRequestNotMerged {
		t.Fatalf("open MR decision/reason = %q/%q", req.Decision, req.Reason)
	}

	if _, err := NormalizeGitLab("X", "d", []byte(`{"object_kind":"tag_push"}`), ""); err == nil {
		t.Fatal("NormalizeGitLab(tag_push) = nil, want error")
	}
}

func TestNormalizeBitbucketPushAndFulfilled(t *testing.T) {
	t.Parallel()

	push := `{"repository":{"uuid":"{u1}","full_name":"team/repo","mainbranch":{"name":"main"}},` +
		`"push":{"changes":[{"new":{"type":"branch","name":"main","target":{"hash":"h1"}},` +
		`"old":{"target":{"hash":"h0"}}}]},"actor":{"nickname":"a"}}`
	req, err := NormalizeBitbucket("repo:push", "d8", []byte(push), "")
	if err != nil {
		t.Fatal(err)
	}
	if req.Decision != DecisionAccepted || req.RepositoryExternalID != "{u1}" || req.TargetSHA != "h1" {
		t.Fatalf("bitbucket push = %+v", req)
	}

	if _, err := NormalizeBitbucket("repo:push", "d", []byte(`{"repository":{},"push":{"changes":[]}}`), ""); err == nil {
		t.Fatal("NormalizeBitbucket(no changes) = nil, want error")
	}

	fulfilled := `{"repository":{"uuid":"{u1}","full_name":"team/repo","mainbranch":{"name":"main"}},` +
		`"pullrequest":{"id":3,"title":"T","destination":{"branch":{"name":"main"},` +
		`"commit":{"hash":"m3"}},"author":{"nickname":"a"}}}`
	req, err = NormalizeBitbucket("pullrequest:fulfilled", "d9", []byte(fulfilled), "")
	if err != nil {
		t.Fatal(err)
	}
	if req.Decision != DecisionAccepted || req.PullRequestNumber != "3" {
		t.Fatalf("bitbucket fulfilled = %+v", req)
	}

	if _, err := NormalizeBitbucket("repo:fork", "d", []byte(`{}`), ""); err == nil {
		t.Fatal("NormalizeBitbucket(repo:fork) = nil, want error")
	}
}

func TestNormalizePagerDutyAndJira(t *testing.T) {
	t.Parallel()

	pd := `{"event":{"id":"e1","event_type":"incident.triggered"},"incident":{"id":"i1"}}`
	req, err := NormalizePagerDuty([]byte(pd), "scope-1")
	if err != nil {
		t.Fatal(err)
	}
	if req.Provider != "pagerduty" || req.EventID != "e1" || req.ScopeID != "scope-1" || req.ResourceID != "i1" {
		t.Fatalf("pagerduty = %+v", req)
	}
	if req.ObservedAt == "" {
		t.Fatal("ObservedAt must be stamped")
	}
	if _, err := NormalizePagerDuty([]byte(pd), ""); err == nil {
		t.Fatal("NormalizePagerDuty(empty scope) = nil, want error")
	}

	jira := `{"webhookEvent":"jira:issue_updated","issueEventId":100,"issue":{"id":"10001","key":"OPS-1"}}`
	jreq, err := NormalizeJira([]byte(jira), "scope-2")
	if err != nil {
		t.Fatal(err)
	}
	if jreq.Provider != "jira" || jreq.EventID != "OPS-1:100" || jreq.ResourceID != "10001" {
		t.Fatalf("jira = %+v", jreq)
	}
	if _, err := NormalizeJira([]byte(`{}`), "scope-2"); err == nil {
		t.Fatal("NormalizeJira(empty) = nil, want error")
	}
}

func TestNormalizeAWSFreshness(t *testing.T) {
	t.Parallel()

	payload := `{"id":"ev-1","detail-type":"EC2 State Change","account":"123","region":"us-east-1",` +
		`"source":"aws.ec2","time":"2026-10-01T00:00:00Z",` +
		`"detail":{"resourceType":"instance","resourceId":"i-1"},"resources":["arn:x"]}`
	req, err := NormalizeAWSFreshness([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	if req.EventID != "ev-1" || req.AccountID != "123" || req.ResourceID != "i-1" || req.ObservedAt != "2026-10-01T00:00:00Z" {
		t.Fatalf("aws = %+v", req)
	}
	if _, err := NormalizeAWSFreshness([]byte(`{"id":"x"}`)); err == nil {
		t.Fatal("NormalizeAWSFreshness(missing fields) = nil, want error")
	}
}

func TestNormalizeGCPFleshness(t *testing.T) {
	t.Parallel()

	payload := `{"message":{"messageId":"m-1","publishTime":"2026-10-01T00:00:00Z",` +
		`"attributes":{"assetType":"compute.googleapis.com/Instance","location":"us-central1",` +
		`"parentScopeKind":"project","parentScopeId":"p1"}}}`
	req, err := NormalizeGCPFleshness([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	if req.EventID != "m-1" || req.ParentScopeID != "p1" || req.Kind != "asset_update" {
		t.Fatalf("gcp = %+v", req)
	}
	if _, err := NormalizeGCPFleshness([]byte(`{"message":{}}`)); err == nil {
		t.Fatal("NormalizeGCPFleshness(missing fields) = nil, want error")
	}
}

func TestRawScalarShapes(t *testing.T) {
	t.Parallel()

	if got := rawScalar([]byte(`42`)); got != "42" {
		t.Fatalf("rawScalar(number) = %q", got)
	}
	if got := rawScalar([]byte(`"x"`)); got != "x" {
		t.Fatalf("rawScalar(string) = %q", got)
	}
	if got := rawScalar([]byte(`null`)); got != "" {
		t.Fatalf("rawScalar(null) = %q", got)
	}
	if got := rawScalar(nil); got != "" {
		t.Fatalf("rawScalar(nil) = %q", got)
	}
}
