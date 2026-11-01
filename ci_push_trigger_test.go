// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import (
	"os"
	"strings"
	"testing"
)

// TestCIWorkflowPushRunsOnMainOnly pins eshu-hq/eshu-webhook#20: every run-*
// gate treats a push event as "run everything", so an unfiltered push
// trigger runs every lane on every branch push, on top of the pull_request
// run that applies the path filters. A docs-only PR must not run any lane,
// which is only true if branch pushes do not trigger this workflow.
func TestCIWorkflowPushRunsOnMainOnly(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var onBlock []string
	inOn := false
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "on:" {
			inOn = true
			continue
		}
		if inOn && line != "" && !strings.HasPrefix(line, " ") {
			break
		}
		if inOn {
			onBlock = append(onBlock, line)
		}
	}
	if len(onBlock) == 0 {
		t.Fatal("ci.yml has no on: block")
	}

	var push []string
	inPush := false
	for _, line := range onBlock {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.HasSuffix(trimmed, ":") || strings.HasPrefix(trimmed, "push:") {
			inPush = strings.HasPrefix(trimmed, "push:")
			if inPush {
				push = append(push, trimmed)
			}
			continue
		}
		if inPush {
			push = append(push, trimmed)
		}
	}
	if len(push) == 0 {
		t.Fatal("ci.yml on: has no push trigger; main merges must still run every lane")
	}
	block := strings.Join(push, "\n")
	if !strings.Contains(block, "branches") || !strings.Contains(block, "main") {
		t.Errorf("push trigger is %q, want a branches filter naming main so branch pushes do not run every lane", block)
	}
}
