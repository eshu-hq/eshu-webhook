// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import (
	"fmt"
	"strconv"
	"strings"
)

// DefaultMaxBodyBytes bounds one provider delivery body: exactly 1 MiB,
// matching the core intake contract (#9377).
const DefaultMaxBodyBytes = int64(1 << 20)

// Config is the satellite's operator configuration. Every secret is read
// from the environment; IntakeURL/IntakeToken are always required because
// the satellite has no storage fallback.
type Config struct {
	GitHubSecret    string
	GitLabToken     string
	BitbucketSecret string
	PagerDutySecret string
	JiraSecret      string

	AWSFreshnessToken string
	GCPFreshnessToken string

	GitHubPath    string
	GitLabPath    string
	BitbucketPath string
	PagerDutyPath string
	JiraPath      string
	AWSPath       string
	GCPPath       string

	PagerDutyScopeID string
	JiraScopeID      string

	MaxRequestBodyBytes int64
	DefaultBranch       string

	// IntakeURL is the core intake API base URL (scheme + host, no route
	// suffix). IntakeToken is the bearer token presented to it. Both are
	// required: the satellite must never silently fall back to direct
	// table writes (it has no tables).
	IntakeURL   string
	IntakeToken string

	ListenAddr string
}

// LoadConfig reads the satellite configuration from getenv. A nil getenv
// fails closed. At least one provider secret or freshness token is required,
// and an intake URL without a token (or vice versa) is rejected.
func LoadConfig(getenv func(string) string) (Config, error) {
	if getenv == nil {
		return Config{}, fmt.Errorf("webhook satellite getenv is required")
	}
	maxBody, err := int64FromEnv(getenv, "ESHU_WEBHOOK_MAX_BODY_BYTES", DefaultMaxBodyBytes)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		GitHubSecret:    strings.TrimSpace(getenv("ESHU_WEBHOOK_GITHUB_SECRET")),
		GitLabToken:     strings.TrimSpace(getenv("ESHU_WEBHOOK_GITLAB_TOKEN")),
		BitbucketSecret: strings.TrimSpace(getenv("ESHU_WEBHOOK_BITBUCKET_SECRET")),
		PagerDutySecret: strings.TrimSpace(getenv("ESHU_WEBHOOK_PAGERDUTY_SECRET")),
		JiraSecret:      strings.TrimSpace(getenv("ESHU_WEBHOOK_JIRA_SECRET")),

		AWSFreshnessToken: strings.TrimSpace(getenv("ESHU_AWS_FRESHNESS_TOKEN")),
		GCPFreshnessToken: strings.TrimSpace(getenv("ESHU_GCP_FRESHNESS_TOKEN")),

		GitHubPath:    firstNonEmpty(strings.TrimSpace(getenv("ESHU_WEBHOOK_GITHUB_PATH")), "/webhooks/github"),
		GitLabPath:    firstNonEmpty(strings.TrimSpace(getenv("ESHU_WEBHOOK_GITLAB_PATH")), "/webhooks/gitlab"),
		BitbucketPath: firstNonEmpty(strings.TrimSpace(getenv("ESHU_WEBHOOK_BITBUCKET_PATH")), "/webhooks/bitbucket"),
		PagerDutyPath: firstNonEmpty(strings.TrimSpace(getenv("ESHU_WEBHOOK_PAGERDUTY_PATH")), "/webhooks/pagerduty"),
		JiraPath:      firstNonEmpty(strings.TrimSpace(getenv("ESHU_WEBHOOK_JIRA_PATH")), "/webhooks/jira"),
		AWSPath:       firstNonEmpty(strings.TrimSpace(getenv("ESHU_AWS_FRESHNESS_PATH")), "/webhooks/aws/eventbridge"),
		GCPPath:       firstNonEmpty(strings.TrimSpace(getenv("ESHU_GCP_FRESHNESS_PATH")), "/webhook/gcp-freshness"),

		PagerDutyScopeID: strings.TrimSpace(getenv("ESHU_WEBHOOK_PAGERDUTY_SCOPE_ID")),
		JiraScopeID:      strings.TrimSpace(getenv("ESHU_WEBHOOK_JIRA_SCOPE_ID")),

		MaxRequestBodyBytes: maxBody,
		DefaultBranch:       strings.TrimSpace(getenv("ESHU_WEBHOOK_DEFAULT_BRANCH")),

		IntakeURL:   strings.TrimSpace(getenv("ESHU_WEBHOOK_INTAKE_URL")),
		IntakeToken: strings.TrimSpace(getenv("ESHU_WEBHOOK_INTAKE_TOKEN")),

		ListenAddr: firstNonEmpty(strings.TrimSpace(getenv("ESHU_WEBHOOK_LISTEN_ADDR")), ":8080"),
	}
	if cfg.GitHubSecret == "" && cfg.GitLabToken == "" && cfg.BitbucketSecret == "" &&
		cfg.PagerDutySecret == "" && cfg.JiraSecret == "" && cfg.AWSFreshnessToken == "" &&
		cfg.GCPFreshnessToken == "" {
		return Config{}, fmt.Errorf("at least one webhook provider secret or freshness token is required")
	}
	if (cfg.IntakeURL == "") != (cfg.IntakeToken == "") {
		return Config{}, fmt.Errorf("webhook intake URL and token must be set together")
	}
	return cfg, nil
}

func int64FromEnv(getenv func(string) string, name string, fallback int64) (int64, error) {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid %s %q: must be a positive integer", name, raw)
	}
	return value, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
