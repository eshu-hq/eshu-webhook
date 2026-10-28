// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Decision and reason literals mirror the core intake contract so receipts
// and submissions share one vocabulary.
const (
	DecisionAccepted = "accepted"
	DecisionIgnored  = "ignored"

	ReasonNonDefaultBranch     = "non_default_branch"
	ReasonTagRef               = "tag_ref"
	ReasonPullRequestNotMerged = "pull_request_not_merged"
	ReasonMissingMergeCommit   = "missing_merge_commit"
	ReasonDeletedBranch        = "deleted_branch"
	ReasonUnsupportedEvent     = "unsupported_event"
)

// NormalizeGitHub converts a verified GitHub event payload into a repository
// intake request. Push maps to event_kind push; pull_request maps to
// pull_request_merged with an ignored decision unless the PR was closed by
// merge with a merge commit. Any other event fails closed.
func NormalizeGitHub(event string, deliveryID string, payload []byte, defaultBranchFallback string) (RepositoryIntakeRequest, error) {
	switch strings.TrimSpace(event) {
	case "push":
		return normalizeGitHubPush(deliveryID, payload, defaultBranchFallback)
	case "pull_request":
		return normalizeGitHubPullRequest(deliveryID, payload, defaultBranchFallback)
	case "workflow_run", "workflow_job":
		return RepositoryIntakeRequest{}, fmt.Errorf("github event %q is an actions identity, use NormalizeGitHubActions", event)
	default:
		return RepositoryIntakeRequest{}, fmt.Errorf("unsupported github webhook event %q", event)
	}
}

func normalizeGitHubPush(deliveryID string, payload []byte, fallback string) (RepositoryIntakeRequest, error) {
	var event struct {
		Ref        string `json:"ref"`
		Before     string `json:"before"`
		After      string `json:"after"`
		Repository struct {
			ID            json.RawMessage `json:"id"`
			FullName      string          `json:"full_name"`
			DefaultBranch string          `json:"default_branch"`
		} `json:"repository"`
		Sender struct {
			Login string `json:"login"`
		} `json:"sender"`
	}
	if err := decodePayload(payload, &event); err != nil {
		return RepositoryIntakeRequest{}, err
	}
	req, err := newRepositoryRequest("github", "push", deliveryID, rawScalar(event.Repository.ID),
		event.Repository.FullName, event.Repository.DefaultBranch, fallback)
	if err != nil {
		return RepositoryIntakeRequest{}, err
	}
	req.Ref = strings.TrimSpace(event.Ref)
	req.BeforeSHA = strings.TrimSpace(event.Before)
	req.TargetSHA = strings.TrimSpace(event.After)
	req.Sender = strings.TrimSpace(event.Sender.Login)
	return finishRepositoryRequest(req)
}

func normalizeGitHubPullRequest(deliveryID string, payload []byte, fallback string) (RepositoryIntakeRequest, error) {
	var event struct {
		Action     string `json:"action"`
		Number     int64  `json:"number"`
		Repository struct {
			ID            json.RawMessage `json:"id"`
			FullName      string          `json:"full_name"`
			DefaultBranch string          `json:"default_branch"`
		} `json:"repository"`
		PullRequest struct {
			Merged         bool   `json:"merged"`
			MergeCommitSHA string `json:"merge_commit_sha"`
			Base           struct {
				Ref string `json:"ref"`
			} `json:"base"`
			HTMLURL string `json:"html_url"`
			Title   string `json:"title"`
		} `json:"pull_request"`
	}
	if err := decodePayload(payload, &event); err != nil {
		return RepositoryIntakeRequest{}, err
	}
	req, err := newRepositoryRequest("github", "pull_request_merged", deliveryID, rawScalar(event.Repository.ID),
		event.Repository.FullName, event.Repository.DefaultBranch, fallback)
	if err != nil {
		return RepositoryIntakeRequest{}, err
	}
	req.Action = strings.TrimSpace(event.Action)
	req.Ref = branchRef(strings.TrimSpace(event.PullRequest.Base.Ref))
	req.TargetSHA = strings.TrimSpace(event.PullRequest.MergeCommitSHA)
	if event.Number > 0 {
		req.PullRequestNumber = strconv.FormatInt(event.Number, 10)
	}
	req.PullRequestURL = strings.TrimSpace(event.PullRequest.HTMLURL)
	req.PullRequestTitle = strings.TrimSpace(event.PullRequest.Title)
	if req.Action != "closed" || !event.PullRequest.Merged {
		req.Decision = DecisionIgnored
		req.Reason = ReasonPullRequestNotMerged
		return checkRepositoryLengths(req)
	}
	if req.TargetSHA == "" {
		req.Decision = DecisionIgnored
		req.Reason = ReasonMissingMergeCommit
		return checkRepositoryLengths(req)
	}
	return finishRepositoryRequest(req)
}

// NormalizeGitLab converts a verified GitLab push or merged merge request
// into a repository intake request.
func NormalizeGitLab(eventHeader string, deliveryID string, payload []byte, defaultBranchFallback string) (RepositoryIntakeRequest, error) {
	var probe struct {
		ObjectKind string `json:"object_kind"`
	}
	if err := decodePayload(payload, &probe); err != nil {
		return RepositoryIntakeRequest{}, err
	}
	switch strings.TrimSpace(probe.ObjectKind) {
	case "", "push":
		return normalizeGitLabPush(deliveryID, payload, defaultBranchFallback)
	case "merge_request":
		return normalizeGitLabMergeRequest(deliveryID, payload, defaultBranchFallback)
	default:
		return RepositoryIntakeRequest{}, fmt.Errorf("unsupported gitlab object_kind %q", probe.ObjectKind)
	}
}

func normalizeGitLabPush(deliveryID string, payload []byte, fallback string) (RepositoryIntakeRequest, error) {
	var event struct {
		Ref     string `json:"ref"`
		Before  string `json:"before"`
		After   string `json:"after"`
		Project struct {
			ID                json.RawMessage `json:"id"`
			PathWithNamespace string          `json:"path_with_namespace"`
			DefaultBranch     string          `json:"default_branch"`
		} `json:"project"`
		UserUsername string `json:"user_username"`
		CheckoutSHA  string `json:"checkout_sha"`
	}
	if err := decodePayload(payload, &event); err != nil {
		return RepositoryIntakeRequest{}, err
	}
	req, err := newRepositoryRequest("gitlab", "push", deliveryID, rawScalar(event.Project.ID),
		event.Project.PathWithNamespace, event.Project.DefaultBranch, fallback)
	if err != nil {
		return RepositoryIntakeRequest{}, err
	}
	req.Ref = strings.TrimSpace(event.Ref)
	req.BeforeSHA = strings.TrimSpace(event.Before)
	req.TargetSHA = firstNonEmpty(strings.TrimSpace(event.After), strings.TrimSpace(event.CheckoutSHA))
	req.Sender = strings.TrimSpace(event.UserUsername)
	return finishRepositoryRequest(req)
}

func normalizeGitLabMergeRequest(deliveryID string, payload []byte, fallback string) (RepositoryIntakeRequest, error) {
	var event struct {
		Project struct {
			ID                json.RawMessage `json:"id"`
			PathWithNamespace string          `json:"path_with_namespace"`
			DefaultBranch     string          `json:"default_branch"`
		} `json:"project"`
		ObjectAttributes struct {
			Action         string `json:"action"`
			State          string `json:"state"`
			MergeCommitSHA string `json:"merge_commit_sha"`
			TargetBranch   string `json:"target_branch"`
			IID            int64  `json:"iid"`
			Title          string `json:"title"`
			URL            string `json:"url"`
		} `json:"object_attributes"`
		User struct {
			Username string `json:"username"`
		} `json:"user"`
	}
	if err := decodePayload(payload, &event); err != nil {
		return RepositoryIntakeRequest{}, err
	}
	req, err := newRepositoryRequest("gitlab", "merge_request_merged", deliveryID, rawScalar(event.Project.ID),
		event.Project.PathWithNamespace, event.Project.DefaultBranch, fallback)
	if err != nil {
		return RepositoryIntakeRequest{}, err
	}
	attrs := event.ObjectAttributes
	req.Action = strings.TrimSpace(attrs.Action)
	req.Ref = branchRef(strings.TrimSpace(attrs.TargetBranch))
	req.TargetSHA = strings.TrimSpace(attrs.MergeCommitSHA)
	req.Sender = strings.TrimSpace(event.User.Username)
	if attrs.IID > 0 {
		req.PullRequestNumber = strconv.FormatInt(attrs.IID, 10)
	}
	req.PullRequestURL = strings.TrimSpace(attrs.URL)
	req.PullRequestTitle = strings.TrimSpace(attrs.Title)
	if strings.TrimSpace(attrs.State) != "merged" {
		req.Decision = DecisionIgnored
		req.Reason = ReasonPullRequestNotMerged
		return checkRepositoryLengths(req)
	}
	if req.TargetSHA == "" {
		req.Decision = DecisionIgnored
		req.Reason = ReasonMissingMergeCommit
		return checkRepositoryLengths(req)
	}
	return finishRepositoryRequest(req)
}

// NormalizeBitbucket converts a verified Bitbucket push or merged pull
// request into a repository intake request. Only the first push change is
// submitted; multi-branch pushes redeliver per branch.
func NormalizeBitbucket(event string, deliveryID string, payload []byte, defaultBranchFallback string) (RepositoryIntakeRequest, error) {
	switch strings.TrimSpace(event) {
	case "repo:push":
		return normalizeBitbucketPush(deliveryID, payload, defaultBranchFallback)
	case "pullrequest:fulfilled":
		return normalizeBitbucketPullRequest(deliveryID, payload, defaultBranchFallback)
	default:
		return RepositoryIntakeRequest{}, fmt.Errorf("unsupported bitbucket event %q", event)
	}
}

func normalizeBitbucketPush(deliveryID string, payload []byte, fallback string) (RepositoryIntakeRequest, error) {
	var event struct {
		Repository struct {
			UUID       string `json:"uuid"`
			FullName   string `json:"full_name"`
			MainBranch struct {
				Name string `json:"name"`
			} `json:"mainbranch"`
		} `json:"repository"`
		Push struct {
			Changes []struct {
				New struct {
					Type   string `json:"type"`
					Name   string `json:"name"`
					Target struct {
						Hash string `json:"hash"`
					} `json:"target"`
				} `json:"new"`
				Old struct {
					Target struct {
						Hash string `json:"hash"`
					} `json:"target"`
				} `json:"old"`
			} `json:"changes"`
		} `json:"push"`
		Actor struct {
			Nickname string `json:"nickname"`
		} `json:"actor"`
	}
	if err := decodePayload(payload, &event); err != nil {
		return RepositoryIntakeRequest{}, err
	}
	if len(event.Push.Changes) == 0 {
		return RepositoryIntakeRequest{}, errors.New("bitbucket push has no changes")
	}
	change := event.Push.Changes[0]
	req, err := newRepositoryRequest("bitbucket", "push", deliveryID, event.Repository.UUID,
		event.Repository.FullName, event.Repository.MainBranch.Name, fallback)
	if err != nil {
		return RepositoryIntakeRequest{}, err
	}
	req.Ref = "refs/heads/" + strings.TrimSpace(change.New.Name)
	req.BeforeSHA = strings.TrimSpace(change.Old.Target.Hash)
	req.TargetSHA = strings.TrimSpace(change.New.Target.Hash)
	req.Sender = strings.TrimSpace(event.Actor.Nickname)
	return finishRepositoryRequest(req)
}

func normalizeBitbucketPullRequest(deliveryID string, payload []byte, fallback string) (RepositoryIntakeRequest, error) {
	var event struct {
		Repository struct {
			UUID       string `json:"uuid"`
			FullName   string `json:"full_name"`
			MainBranch struct {
				Name string `json:"name"`
			} `json:"mainbranch"`
		} `json:"repository"`
		PullRequest struct {
			ID          int64  `json:"id"`
			Title       string `json:"title"`
			Destination struct {
				Branch struct {
					Name string `json:"name"`
				} `json:"branch"`
				Commit struct {
					Hash string `json:"hash"`
				} `json:"commit"`
			} `json:"destination"`
			Author struct {
				Nickname string `json:"nickname"`
			} `json:"author"`
		} `json:"pullrequest"`
	}
	if err := decodePayload(payload, &event); err != nil {
		return RepositoryIntakeRequest{}, err
	}
	pr := event.PullRequest
	req, err := newRepositoryRequest("bitbucket", "pull_request_merged", deliveryID, event.Repository.UUID,
		event.Repository.FullName, event.Repository.MainBranch.Name, fallback)
	if err != nil {
		return RepositoryIntakeRequest{}, err
	}
	req.Action = "fulfilled"
	req.Ref = branchRef(strings.TrimSpace(pr.Destination.Branch.Name))
	req.TargetSHA = strings.TrimSpace(pr.Destination.Commit.Hash)
	req.Sender = strings.TrimSpace(pr.Author.Nickname)
	if pr.ID > 0 {
		req.PullRequestNumber = strconv.FormatInt(pr.ID, 10)
	}
	req.PullRequestTitle = strings.TrimSpace(pr.Title)
	if req.TargetSHA == "" {
		req.Decision = DecisionIgnored
		req.Reason = ReasonMissingMergeCommit
		return checkRepositoryLengths(req)
	}
	return finishRepositoryRequest(req)
}

func newRepositoryRequest(provider, eventKind, deliveryID, externalID, fullName, defaultBranch, fallback string) (RepositoryIntakeRequest, error) {
	req := RepositoryIntakeRequest{
		APIVersion:           APIVersionV1,
		Provider:             provider,
		DeliveryID:           strings.TrimSpace(deliveryID),
		EventKind:            eventKind,
		Decision:             DecisionAccepted,
		RepositoryExternalID: strings.TrimSpace(externalID),
		RepositoryFullName:   strings.TrimSpace(fullName),
		DefaultBranch:        firstNonEmpty(strings.TrimSpace(defaultBranch), strings.TrimSpace(fallback)),
	}
	if req.DeliveryID == "" || req.RepositoryExternalID == "" || req.RepositoryFullName == "" ||
		req.DefaultBranch == "" {
		return RepositoryIntakeRequest{}, errors.New("repository trigger is missing required identity fields")
	}
	if err := CheckWireLengths(map[string]string{
		"delivery_id": req.DeliveryID, "repository_external_id": req.RepositoryExternalID,
		"repository_full_name": req.RepositoryFullName, "default_branch": req.DefaultBranch,
	}); err != nil {
		return RepositoryIntakeRequest{}, err
	}
	return req, nil
}

// wireFieldMap exposes every intake string field so the length bound
// covers fields set after the identity check (ref, SHAs, sender, action,
// PR fields), not only the four identity fields checked at construction.
func (r RepositoryIntakeRequest) wireFieldMap() map[string]string {
	return map[string]string{
		"api_version": r.APIVersion, "provider": r.Provider,
		"delivery_id": r.DeliveryID, "event_kind": r.EventKind,
		"decision": r.Decision, "reason": r.Reason,
		"repository_external_id": r.RepositoryExternalID,
		"repository_full_name":   r.RepositoryFullName,
		"default_branch":         r.DefaultBranch,
		"ref":                    r.Ref,
		"before_sha":             r.BeforeSHA,
		"target_sha":             r.TargetSHA,
		"action":                 r.Action,
		"sender":                 r.Sender,
		"pull_request_number":    r.PullRequestNumber,
		"pull_request_url":       r.PullRequestURL,
		"pull_request_title":     r.PullRequestTitle,
	}
}

// finishRepositoryRequest applies the branch decision, then bounds every
// wire field. The decision runs first so tag/off-branch/deleted-branch
// deliveries keep their ignored reason; the length check runs last so no
// oversized field reaches the intake uplink.
func finishRepositoryRequest(req RepositoryIntakeRequest) (RepositoryIntakeRequest, error) {
	return checkRepositoryLengths(decideBranchRequest(req))
}

// checkRepositoryLengths bounds every wire field of an already-decided
// request. Ignored-decision early returns use this directly so oversized
// fields in unmerged or SHA-less PRs still fail closed before submission.
func checkRepositoryLengths(req RepositoryIntakeRequest) (RepositoryIntakeRequest, error) {
	if err := CheckWireLengths(req.wireFieldMap()); err != nil {
		return RepositoryIntakeRequest{}, err
	}
	return req, nil
}

// decideBranchRequest mirrors core's branch decision: tag refs, non-default
// branches, and deleted branches are valid but ignored; only a default
// branch push with a target SHA is accepted.
func decideBranchRequest(req RepositoryIntakeRequest) RepositoryIntakeRequest {
	if isTagRef(req.Ref) {
		req.Decision = DecisionIgnored
		req.Reason = ReasonTagRef
		return req
	}
	branch, ok := branchFromRef(req.Ref)
	if !ok || branch != req.DefaultBranch {
		req.Decision = DecisionIgnored
		req.Reason = ReasonNonDefaultBranch
		return req
	}
	if req.TargetSHA == "" {
		req.Decision = DecisionIgnored
		req.Reason = ReasonMissingMergeCommit
		return req
	}
	if isZeroSHA(req.TargetSHA) {
		req.Decision = DecisionIgnored
		req.Reason = ReasonDeletedBranch
		return req
	}
	req.Decision = DecisionAccepted
	return req
}

func decodePayload(payload []byte, target any) error {
	if len(strings.TrimSpace(string(payload))) == 0 {
		return errors.New("webhook payload is empty")
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return fmt.Errorf("decode webhook payload: %w", err)
	}
	return nil
}

func isTagRef(ref string) bool {
	return strings.HasPrefix(ref, "refs/tags/")
}

func branchFromRef(ref string) (string, bool) {
	const prefix = "refs/heads/"
	if !strings.HasPrefix(ref, prefix) {
		return "", false
	}
	branch := strings.TrimPrefix(ref, prefix)
	if branch == "" {
		return "", false
	}
	return branch, true
}

func branchRef(branch string) string {
	if branch == "" {
		return ""
	}
	return "refs/heads/" + branch
}

func isZeroSHA(sha string) bool {
	if sha == "" {
		return false
	}
	for i := 0; i < len(sha); i++ {
		if sha[i] != '0' {
			return false
		}
	}
	return true
}

func rawScalar(raw json.RawMessage) string {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		return strings.TrimSpace(number.String())
	}
	return ""
}
