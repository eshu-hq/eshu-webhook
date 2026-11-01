// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package webhook

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// This file freezes the CI workflow contract after the lane bodies moved to
// the pinned eshu-hq/.github reusable. The module carries no dependencies, so
// the workflow is read with plain string checks instead of a YAML library.

const (
	ciWorkflowPath = ".github/workflows/ci.yml"

	// wantUses is the pinned reusable: the v2 merge commit, never the tag.
	// Tests that reason about reusable behavior derive from this pin, so a
	// pin move must be re-verified against the new body.
	wantUses = "eshu-hq/.github/.github/workflows/go-collector-ci.yml@4d01ab90baf5dd77c21cc1ee6bdf701332613449"
)

var (
	pinnedAction  = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._/-]+@[0-9a-f]{40}$`)
	jobHeader     = regexp.MustCompile(`(?m)^  ([a-z][a-z-]*):[ \t]*$`)
	secretUse     = regexp.MustCompile(`secrets\.([A-Za-z0-9_]+)`)
	floorLiteral  = regexp.MustCompile(`COVERAGE_FLOOR\s*\??=\s*["']?([0-9.]+)["']?`)
	trailingNote  = regexp.MustCompile(`\s+#.*$`)
	callerJobs    = []string{"changes", "lanes", "scheduled-findings", "gosec-sarif", "ci-complete"}
	wantTimeouts  = map[string]string{"changes": "5", "scheduled-findings": "5", "gosec-sarif": "5", "ci-complete": "5"}
	requiredPerms = map[string]string{"scheduled-findings": "issues: write", "gosec-sarif": "security-events: write"}
)

// ciWorkflow is ci.yml with comments removed, split into its job blocks.
type ciWorkflow struct {
	raw  string
	text string
	jobs map[string]string
}

func loadCIWorkflow(t *testing.T) ciWorkflow {
	t.Helper()

	raw, err := os.ReadFile(ciWorkflowPath)
	if err != nil {
		t.Fatal(err)
	}
	return parseCIWorkflow(t, string(raw))
}

func parseCIWorkflow(t *testing.T, raw string) ciWorkflow {
	t.Helper()

	var kept []string
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "#") {
			continue
		}
		kept = append(kept, trailingNote.ReplaceAllString(line, ""))
	}
	text := strings.Join(kept, "\n")
	_, jobsText, ok := strings.Cut(text, "\njobs:\n")
	if !ok {
		t.Fatal("ci.yml defines no jobs")
	}
	jobs := map[string]string{}
	headers := jobHeader.FindAllStringSubmatchIndex(jobsText, -1)
	for i, h := range headers {
		end := len(jobsText)
		if i+1 < len(headers) {
			end = headers[i+1][0]
		}
		jobs[jobsText[h[2]:h[3]]] = jobsText[h[1]:end]
	}
	return ciWorkflow{raw: raw, text: text, jobs: jobs}
}

// field returns the first single-line `key: value` in block, or "".
func field(block, key string) string {
	re := regexp.MustCompile(`(?m)^\s+` + regexp.QuoteMeta(key) + `:[ \t]*(\S.*)$`)
	m := re.FindStringSubmatch(block)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func (w ciWorkflow) job(t *testing.T, name string) string {
	t.Helper()

	block, ok := w.jobs[name]
	if !ok {
		t.Fatalf("ci.yml has no %q job", name)
	}
	return block
}

// TestCIWorkflowIsThinCallerOfPinnedReusable keeps the lane bodies upstream:
// the caller holds exactly its five jobs, `lanes` calls the SHA-pinned v2
// reusable, and the tag rides only as a trailing comment.
func TestCIWorkflowIsThinCallerOfPinnedReusable(t *testing.T) {
	t.Parallel()

	w := loadCIWorkflow(t)
	var got []string
	for name := range w.jobs {
		got = append(got, name)
	}
	sort.Strings(got)
	want := append([]string(nil), callerJobs...)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ci.yml jobs = %v, want exactly %v: lane bodies live in the reusable", got, want)
	}
	lanes := w.job(t, "lanes")
	if uses := field(lanes, "uses"); uses != wantUses {
		t.Errorf("lanes uses %q, want the pinned reusable %q", uses, wantUses)
	}
	if !strings.Contains(w.raw, wantUses+" # v2") {
		t.Errorf("lanes pin lacks the trailing %q version comment", "# v2")
	}
	if strings.Contains(lanes, "steps:") {
		t.Error("lanes carries inline steps, want a pure reusable call")
	}
}

// TestCIWorkflowPinsActionsByCommitSHA keeps a moved tag from changing what
// pull-request CI executes.
func TestCIWorkflowPinsActionsByCommitSHA(t *testing.T) {
	t.Parallel()

	w := loadCIWorkflow(t)
	uses := regexp.MustCompile(`(?m)^\s+(?:- )?uses:[ \t]*(\S+)`).FindAllStringSubmatch(w.text, -1)
	if len(uses) == 0 {
		t.Fatal("ci.yml has no uses: lines")
	}
	for _, m := range uses {
		if !pinnedAction.MatchString(m[1]) {
			t.Errorf("uses %q, want a full commit SHA pin", m[1])
		}
	}
}

// effectiveGates pins every run gate to the exact expression the retired
// inline lane used. Schedule runs keep lint/test/race/shell/image/actionlint
// off and govulncheck/gosec on, as before the move. run-boundary mirrors the
// retired `test` lane, which held the boundary tests as a step.
var effectiveGates = map[string]string{
	"run-lint":        "${{ github.event_name == 'push' || (github.event_name == 'pull_request' && (needs.changes.outputs.go == 'true' || needs.changes.outputs.misc == 'true' || needs.changes.outputs.workflows == 'true')) }}",
	"run-test":        "${{ github.event_name == 'push' || (github.event_name == 'pull_request' && (needs.changes.outputs.go == 'true' || needs.changes.outputs.fixtures == 'true' || needs.changes.outputs.misc == 'true' || needs.changes.outputs.workflows == 'true')) }}",
	"run-race":        "${{ github.event_name == 'push' || (github.event_name == 'pull_request' && (needs.changes.outputs.go == 'true' || needs.changes.outputs.misc == 'true' || needs.changes.outputs.workflows == 'true')) }}",
	"run-shell":       "${{ github.event_name == 'push' || (github.event_name == 'pull_request' && needs.changes.outputs.shell == 'true') }}",
	"run-image":       "${{ github.event_name == 'push' || (github.event_name == 'pull_request' && (needs.changes.outputs.image == 'true' || needs.changes.outputs.go == 'true' || needs.changes.outputs.workflows == 'true')) }}",
	"run-boundary":    "${{ github.event_name == 'push' || (github.event_name == 'pull_request' && (needs.changes.outputs.go == 'true' || needs.changes.outputs.fixtures == 'true' || needs.changes.outputs.misc == 'true' || needs.changes.outputs.workflows == 'true')) }}",
	"run-govulncheck": "${{ github.event_name == 'push' || github.event_name == 'schedule' || (github.event_name == 'pull_request' && (needs.changes.outputs.deps == 'true' || needs.changes.outputs.workflows == 'true')) }}",
	"run-gosec":       "${{ github.event_name == 'push' || github.event_name == 'schedule' || (github.event_name == 'pull_request' && (needs.changes.outputs.go == 'true' || needs.changes.outputs.workflows == 'true')) }}",
	"run-actionlint":  "${{ github.event_name == 'push' || (github.event_name == 'pull_request' && needs.changes.outputs.workflows == 'true') }}",
	// The retired gosec lane failed PR and push runs on any finding; the
	// reusable only fails when asked, so the input stays unconditionally on.
	"gosec-fail-on-findings": "true",
	"boundary-command":       "go test . -run 'TestImportBoundary|TestBodyOverLimit|TestDefaultBodyLimit|TestWireFieldLimit|TestEmptyPayload|TestMalformedJSON' -count=1 -v",
}

// TestCIWorkflowLaneGatesMatchRetiredConditions fails when any gate is
// flipped, widened, or dropped, so lane selection cannot go dark unnoticed.
func TestCIWorkflowLaneGatesMatchRetiredConditions(t *testing.T) {
	t.Parallel()

	lanes := loadCIWorkflow(t).job(t, "lanes")
	for input, want := range effectiveGates {
		if got := field(lanes, input); got != want {
			t.Errorf("lanes input %q = %q, want the exact retired gate %q", input, got, want)
		}
	}
}

// TestCIWorkflowPinsToolsAndFloor keeps every tool pin and the coverage
// floor the inline lanes held: the move must neither downgrade nor silently
// upgrade a scanner.
func TestCIWorkflowPinsToolsAndFloor(t *testing.T) {
	t.Parallel()

	lanes := loadCIWorkflow(t).job(t, "lanes")
	for input, want := range map[string]string{
		"module":                "webhook",
		"go-version-file":       "go.mod",
		"coverage-floor":        "90.0",
		"golangci-lint-version": "v2.12.2",
		"govulncheck-version":   "v1.9.0",
		"gosec-version":         "v2.30.0",
		"actionlint-version":    "1.7.12",
		"actionlint-sha256":     "8aca8db96f1b94770f1b0d72b6dddcb1ebb8123cb3712530b08cc387b349a3d8",
	} {
		if got := field(lanes, input); got != want {
			t.Errorf("lanes input %q = %q, want %q", input, got, want)
		}
	}
}

// TestCoverageFloorParity keeps `make ci` honest: the Makefile floor and the
// lanes coverage-floor input must name the same number, so a ratchet that
// touches one file and not the other fails here.
func TestCoverageFloorParity(t *testing.T) {
	t.Parallel()

	makefile, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatal(err)
	}
	m := floorLiteral.FindStringSubmatch(string(makefile))
	ci := field(loadCIWorkflow(t).job(t, "lanes"), "coverage-floor")
	if m == nil || ci == "" {
		t.Fatalf("COVERAGE_FLOOR literal missing (Makefile=%q lanes=%q)", m, ci)
	}
	if m[1] != ci {
		t.Errorf("COVERAGE_FLOOR drift: Makefile=%s lanes=%s, want one shared value", m[1], ci)
	}
}

// TestCIWorkflowImageLaneInputs keeps the image lane's build tag and the
// Dockerfile digest-pin proof the inline lane ran.
func TestCIWorkflowImageLaneInputs(t *testing.T) {
	t.Parallel()

	lanes := loadCIWorkflow(t).job(t, "lanes")
	for input, want := range map[string]string{
		"image-tag":     "ci-webhook:ci",
		"image-context": ".",
	} {
		if got := field(lanes, input); got != want {
			t.Errorf("lanes input %q = %q, want %q", input, got, want)
		}
	}
	for _, want := range []string{
		"image-command: |",
		"set -euo pipefail",
		"grep -Ec '^[[:space:]]*FROM ' Dockerfile",
		"grep -Ec '^[[:space:]]*FROM [^[:space:]]+@sha256:[0-9a-f]{64}' Dockerfile",
		`[ "$from_total" -ge 1 ] && [ "$from_pinned" -eq "$from_total" ]`,
	} {
		if !strings.Contains(lanes, want) {
			t.Errorf("lanes image-command lacks %q", want)
		}
	}
}

// TestCIWorkflowChangesRunsOnEveryEvent keeps `changes` unguarded: every lane
// needs it, so an `if:` that skips it on schedule or push skips every lane
// and the run goes vacantly green.
func TestCIWorkflowChangesRunsOnEveryEvent(t *testing.T) {
	t.Parallel()

	w := loadCIWorkflow(t)
	if got := field(w.job(t, "changes"), "if"); got != "" {
		t.Errorf("changes carries if: %q, want none: every lane needs it on every event", got)
	}
	if got := field(w.job(t, "lanes"), "needs"); got != "[changes]" {
		t.Errorf("lanes needs %q, want [changes]", got)
	}
}

// TestCIWorkflowCallerSideScanJobs keeps the two behaviors the reusable
// cannot own: the weekly findings issue and the Security-tab SARIF upload.
func TestCIWorkflowCallerSideScanJobs(t *testing.T) {
	t.Parallel()

	w := loadCIWorkflow(t)
	findings := w.job(t, "scheduled-findings")
	if got := field(findings, "if"); got != "failure() && github.event_name == 'schedule'" {
		t.Errorf("scheduled-findings if = %q, want failure() on schedule only", got)
	}
	sarif := w.job(t, "gosec-sarif")
	if got := field(sarif, "if"); got != "github.event_name != 'pull_request'" {
		t.Errorf("gosec-sarif if = %q, want non-PR events only (forks cannot write code scanning)", got)
	}
	for name, block := range map[string]string{"scheduled-findings": findings, "gosec-sarif": sarif} {
		if got := field(block, "needs"); got != "[lanes]" {
			t.Errorf("%s needs %q, want [lanes]", name, got)
		}
		if !strings.Contains(block, requiredPerms[name]) {
			t.Errorf("%s lacks permission %q", name, requiredPerms[name])
		}
	}
	for _, want := range []string{"name: gosec-webhook", "continue-on-error: true", "sarif_file: gosec.sarif"} {
		if !strings.Contains(sarif, want) {
			t.Errorf("gosec-sarif lacks %q", want)
		}
	}
}

// TestCIWorkflowSetsJobTimeouts keeps a hung runner from holding a PR open.
// The lanes reusable call carries none: the schema forbids timeout-minutes
// there and each inner lane sets its own (lint/race/image 15, test/
// govulncheck/boundary 10, gosec 30, shell/actionlint 5 at the pin).
func TestCIWorkflowSetsJobTimeouts(t *testing.T) {
	t.Parallel()

	w := loadCIWorkflow(t)
	for name, block := range w.jobs {
		got := field(block, "timeout-minutes")
		if name == "lanes" {
			if got != "" {
				t.Errorf("lanes sets timeout-minutes %q: the schema forbids it on reusable calls", got)
			}
			continue
		}
		if want := wantTimeouts[name]; got != want {
			t.Errorf("job %s timeout-minutes = %q, want %q", name, got, want)
		}
	}
}

// TestCIWorkflowKeepsTokenScopeAndConcurrency keeps fork PRs working: no
// configured secret beyond the automatic token, contents: read at the top,
// and superseded pull-request runs cancelled.
func TestCIWorkflowKeepsTokenScopeAndConcurrency(t *testing.T) {
	t.Parallel()

	w := loadCIWorkflow(t)
	head, _, _ := strings.Cut(w.text, "\njobs:\n")
	if !strings.Contains(head, "permissions:\n  contents: read\n") {
		t.Error("top-level permissions lack contents: read")
	}
	if !strings.Contains(head, "github.event.pull_request.number") ||
		!strings.Contains(head, "cancel-in-progress: ${{ github.event_name == 'pull_request' }}") {
		t.Error("concurrency must group per PR and cancel superseded pull-request runs only")
	}
	for _, m := range secretUse.FindAllStringSubmatch(w.text, -1) {
		if m[1] != "GITHUB_TOKEN" {
			t.Errorf("uses configured secret %q, want none but GITHUB_TOKEN", m[1])
		}
	}
}

// aggregatorProblems returns every way run breaks the ci-complete contract:
// compare each job's result (whole-object comparison never matches, a bare
// needs.*.result array breaks green runs), fail failed and cancelled lanes,
// and say skips are allowed.
func aggregatorProblems(run string) []string {
	var problems []string
	if !strings.Contains(run, `v["result"]`) && !strings.Contains(run, `.get("result")`) {
		problems = append(problems, `aggregator must compare v["result"] per job, not whole job objects (false-green)`)
	}
	if strings.Contains(run, "join(needs.*.result") {
		problems = append(problems, "aggregator must not expand needs.*.result (bare array breaks green runs)")
	}
	for _, want := range []string{`"failure"`, `"cancelled"`, "skip"} {
		if !strings.Contains(run, want) {
			problems = append(problems, "aggregator never mentions "+want)
		}
	}
	return problems
}

// TestCICompleteAggregatesPerJobResults pins the aggregator shape against its
// two historical false modes. The live ci.yml must pass; both mutants must
// fail.
func TestCICompleteAggregatesPerJobResults(t *testing.T) {
	t.Parallel()

	complete := loadCIWorkflow(t).job(t, "ci-complete")
	if got := field(complete, "if"); got != "always()" {
		t.Errorf("ci-complete if = %q, want always()", got)
	}
	if got := field(complete, "needs"); got != "[changes, lanes, scheduled-findings, gosec-sarif]" {
		t.Errorf("ci-complete needs %q, want every other job", got)
	}
	if got := field(complete, "NEEDS_JSON"); got != "${{ toJSON(needs) }}" {
		t.Errorf("ci-complete NEEDS_JSON = %q, want toJSON(needs)", got)
	}
	if problems := aggregatorProblems(complete); len(problems) > 0 {
		t.Errorf("ci-complete aggregator breaks its contract:\n%s", strings.Join(problems, "\n"))
	}
	for name, run := range map[string]string{
		"bare needs array": "RESULTS: ${{ join(needs.*.result, ' ') }}\nfor result in $RESULTS; do [ \"$result\" = success ] || exit 1; done",
		"whole objects":    `bad = {k: v for k, v in needs.items() if v == "failure"}`,
	} {
		if len(aggregatorProblems(run)) == 0 {
			t.Errorf("%s: the gate accepted a violating aggregator", name)
		}
	}
}
