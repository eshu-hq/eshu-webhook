// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package main

import (
	"strings"
	"testing"
)

func TestRunFailsClosedWithoutConfig(t *testing.T) {
	if err := run(func(string) string { return "" }); err == nil {
		t.Fatal("run(empty env) = nil, want error")
	}
	if err := run(nil); err == nil {
		t.Fatal("run(nil getenv) = nil, want error")
	}
}

func TestRunFailsClosedOnBadListenAddr(t *testing.T) {
	err := run(func(key string) string {
		switch key {
		case "ESHU_WEBHOOK_GITHUB_SECRET":
			return "s"
		case "ESHU_WEBHOOK_INTAKE_URL":
			return "http://127.0.0.1:1"
		case "ESHU_WEBHOOK_INTAKE_TOKEN":
			return "tok"
		case "ESHU_WEBHOOK_LISTEN_ADDR":
			return "http://bad url"
		}
		return ""
	})
	if err == nil {
		t.Fatal("run(bad listen addr) = nil, want error")
	}
	if !strings.Contains(err.Error(), "bad") && !strings.Contains(err.Error(), "address") &&
		!strings.Contains(err.Error(), "listen") && !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("run error = %v, want an address error", err)
	}
}
