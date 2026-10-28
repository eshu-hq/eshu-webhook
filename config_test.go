// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import (
	"testing"
)

func envOf(pairs map[string]string) func(string) string {
	return func(key string) string {
		return pairs[key]
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := LoadConfig(envOf(map[string]string{
		"ESHU_WEBHOOK_GITHUB_SECRET": "s",
		"ESHU_WEBHOOK_INTAKE_URL":    "https://core:8443",
		"ESHU_WEBHOOK_INTAKE_TOKEN":  "tok",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GitHubPath != "/webhooks/github" || cfg.ListenAddr != ":8080" {
		t.Fatalf("defaults = %+v", cfg)
	}
	if cfg.MaxRequestBodyBytes != DefaultMaxBodyBytes {
		t.Fatalf("MaxRequestBodyBytes = %d", cfg.MaxRequestBodyBytes)
	}
}

func TestLoadConfigRejectsIncomplete(t *testing.T) {
	t.Parallel()

	if _, err := LoadConfig(nil); err == nil {
		t.Fatal("LoadConfig(nil) = nil, want error")
	}
	if _, err := LoadConfig(envOf(nil)); err == nil {
		t.Fatal("LoadConfig(no secrets) = nil, want error")
	}
	if _, err := LoadConfig(envOf(map[string]string{
		"ESHU_WEBHOOK_GITHUB_SECRET": "s",
		"ESHU_WEBHOOK_INTAKE_URL":    "https://core:8443",
	})); err == nil {
		t.Fatal("LoadConfig(intake URL without token) = nil, want error")
	}
	if _, err := LoadConfig(envOf(map[string]string{
		"ESHU_WEBHOOK_GITHUB_SECRET": "s",
		"ESHU_WEBHOOK_INTAKE_TOKEN":  "tok",
	})); err == nil {
		t.Fatal("LoadConfig(intake token without URL) = nil, want error")
	}
	if _, err := LoadConfig(envOf(map[string]string{
		"ESHU_WEBHOOK_GITHUB_SECRET":  "s",
		"ESHU_WEBHOOK_MAX_BODY_BYTES": "huge",
		"ESHU_WEBHOOK_INTAKE_URL":     "https://core:8443",
		"ESHU_WEBHOOK_INTAKE_TOKEN":   "tok",
	})); err == nil {
		t.Fatal("LoadConfig(bad body cap) = nil, want error")
	}
	if _, err := LoadConfig(envOf(map[string]string{
		"ESHU_WEBHOOK_GITHUB_SECRET":  "s",
		"ESHU_WEBHOOK_MAX_BODY_BYTES": "-5",
	})); err == nil {
		t.Fatal("LoadConfig(negative body cap) = nil, want error")
	}
}

func TestLoadConfigHonorsOverrides(t *testing.T) {
	t.Parallel()

	cfg, err := LoadConfig(envOf(map[string]string{
		"ESHU_WEBHOOK_GITLAB_TOKEN":   "t",
		"ESHU_WEBHOOK_GITHUB_PATH":    "/g",
		"ESHU_WEBHOOK_DEFAULT_BRANCH": "trunk",
		"ESHU_WEBHOOK_MAX_BODY_BYTES": "65536",
		"ESHU_WEBHOOK_LISTEN_ADDR":    "127.0.0.1:9090",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GitHubPath != "/g" || cfg.DefaultBranch != "trunk" || cfg.MaxRequestBodyBytes != 65536 {
		t.Fatalf("overrides = %+v", cfg)
	}
}

func TestNewServerRequiresUplink(t *testing.T) {
	t.Parallel()

	cfg := testConfig()
	cfg.IntakeURL = ""
	cfg.IntakeToken = ""
	if _, err := NewServer(cfg, nil); err == nil {
		t.Fatal("NewServer(no uplink) = nil, want error")
	}
	cfg = testConfig()
	cfg.IntakeURL = "://bad"
	if _, err := NewServer(cfg, nil); err == nil {
		t.Fatal("NewServer(bad URL) = nil, want error")
	}
}
