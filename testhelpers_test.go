// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testConfig returns a server configuration that passes construction
// without contacting core: the intake uplink points at a loopback URL that
// NewServerWithClient tests override per case.
func testConfig() Config {
	return Config{
		GitHubSecret:        "test-github-secret",
		GitHubPath:          "/webhooks/github",
		GitLabPath:          "/webhooks/gitlab",
		BitbucketPath:       "/webhooks/bitbucket",
		PagerDutyPath:       "/webhooks/pagerduty",
		JiraPath:            "/webhooks/jira",
		AWSPath:             "/webhooks/aws/eventbridge",
		GCPPath:             "/webhook/gcp-freshness",
		MaxRequestBodyBytes: DefaultMaxBodyBytes,
		DefaultBranch:       "main",
		IntakeURL:           "http://127.0.0.1:1",
		IntakeToken:         "test-token",
		ListenAddr:          "127.0.0.1:0",
	}
}

// isStandardOrRelative reports whether importPath is standard library,
// relative, or explicitly allowed for this module.
func isStandardOrRelative(importPath, modulePath string) bool {
	if strings.HasPrefix(importPath, modulePath) {
		return true
	}
	// Standard library imports never contain a dot in the first segment.
	first, _, _ := strings.Cut(importPath, "/")
	return !strings.Contains(first, ".")
}

// moduleRoot returns the repository root (the directory holding go.mod).
func moduleRoot(t *testing.T) (string, error) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above working directory")
		}
		dir = parent
	}
}

// readRepoFile reads a repository-relative file for contract tests (CI
// workflow shape, Dockerfile pins).
func readRepoFile(t *testing.T, name string) (string, error) {
	t.Helper()
	root, err := moduleRoot(t)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
