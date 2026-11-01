#!/usr/bin/env bash
# Seeded RED/GREEN matrix for the locally provable CI lanes.
#
# Local runner, not a CI gate: for each lane it plants a defect, asserts the
# lane command turns red AND names the seed file, removes the seed, and
# asserts the lane turns green again. It never compares the tree against a
# committed count or list.
#
# 14 checks: vet, lint, format, race, tidy, the 500-line cap, and the
# coverage floor (RED + GREEN each). The lane commands below are the ones
# the Makefile (`make ci`) and the pinned eshu-hq/.github go-collector-ci
# reusable run, copied verbatim. The lane bodies that live upstream (the
# ci-complete aggregation, the govulncheck/gosec reachability proofs) are
# proven by that repo's own selftest and are not reseeded here.
#
# Usage: bash scripts/webhook-seed-matrix.sh
# Needs: go (version in go.mod), golangci-lint v2.12.2, git, find, awk.
# Every seed file starts with zz_seed_ and is removed on every exit path;
# the run fails if `git status --porcelain` differs from the start.
set -euo pipefail

cd "$(dirname "$0")/.."

TMP="$(mktemp -d)"
LOG="$TMP/last.log"
PROFILE="$TMP/coverage.out"
GOMOD_BACKUP="$TMP/go.mod.orig"
pass=0
fail=0

cleanup() {
	rm -f zz_seed_*.go
	if [ -f "$GOMOD_BACKUP" ]; then
		cp "$GOMOD_BACKUP" go.mod
	fi
	rm -rf "$TMP"
}
trap cleanup EXIT

# Refuse to seed on top of leftovers or a drifted go.mod: the cleanup would
# otherwise delete or overwrite work that is not ours.
if compgen -G 'zz_seed_*' >/dev/null; then
	echo "FAIL: leftover seed files present; refusing to seed"
	ls zz_seed_*
	exit 1
fi
if ! git diff --exit-code -- go.mod >/dev/null 2>&1; then
	echo "FAIL: go.mod already drifted; refusing to seed"
	exit 1
fi
cp go.mod "$GOMOD_BACKUP"
SNAPSHOT="$TMP/status-before.txt"
git status --porcelain >"$SNAPSHOT"

# --- lane commands: verbatim from Makefile `lint`/`race` and go-collector-ci ---
lane_vet() { go vet ./...; }
lane_lint() { golangci-lint run ./...; }
lane_format() { golangci-lint fmt --diff ./...; }
lane_tidy() {
	go mod tidy -diff
	git diff --exit-code -- go.mod go.sum
}
lane_cap() {
	over=$(find . -name '*.go' -not -path './vendor/*' -exec wc -l {} + | awk '$1 > 500 && $2 != "total" {print}')
	if [ -n "$over" ]; then
		echo "$over"
		echo "FAIL: .go files over 500 lines"
		return 1
	fi
}
lane_race() { go test ./... -race -count=1 -covermode=atomic -coverprofile="$PROFILE"; }
lane_floor() { # $1: coverage floor
	total=$(go tool cover -func="$PROFILE" | awk '/^total:/ {gsub(/%/, "", $3); print $3}')
	echo "total=$total floor=$1"
	awk -v t="$total" -v f="$1" 'BEGIN { if (t + 0 < f + 0) { print "FAIL: coverage below floor"; exit 1 } }'
}

ok() { # $1: check name
	echo "PASS: $1"
	pass=$((pass + 1))
}
bad() { # $1: check name
	echo "FAIL: $1"
	tail -n 20 "$LOG" 2>/dev/null || true
	fail=$((fail + 1))
}

# red NAME NEEDLE LANE [ARGS...]: the lane must fail and its output must
# contain NEEDLE (the seed file name, or the finding it plants).
red() {
	local name=$1 needle=$2
	shift 2
	if "$@" >"$LOG" 2>&1; then
		bad "$name RED (lane stayed green)"
	elif [[ "$(<"$LOG")" == *"$needle"* ]]; then
		ok "$name RED"
	else
		bad "$name RED (lane failed without naming $needle)"
	fi
}

# green NAME LANE [ARGS...]: the lane must pass.
green() {
	local name=$1
	shift
	if "$@" >"$LOG" 2>&1; then
		ok "$name GREEN after seed removal"
	else
		bad "$name GREEN after seed removal"
	fi
}

# --- vet: seeded printf mismatch turns the vet step red ---
cat >zz_seed_vet.go <<'EOF'
package webhook

import "fmt"

var _ = fmt.Sprintf("%d", "not-a-number")
EOF
red "vet (seeded printf mismatch)" "zz_seed_vet.go" lane_vet
rm -f zz_seed_vet.go
green "vet" lane_vet

# --- lint: seeded strings.Replace with -1 trips staticcheck QF1004 ---
cat >zz_seed_lint.go <<'EOF'
package webhook

import "strings"

var _ = strings.Replace("aaa", "a", "b", -1)
EOF
red "lint (seeded QF1004)" "zz_seed_lint.go" lane_lint
rm -f zz_seed_lint.go
green "lint" lane_lint

# --- format: seeded unformatted file trips the gofumpt diff check ---
printf 'package webhook\n\nfunc zzSeedFormat() { x:=1; _=x }\n' >zz_seed_format.go
red "format (seeded unformatted file)" "zz_seed_format.go" lane_format
rm -f zz_seed_format.go
green "format" lane_format

# --- race: seeded data race fails the race lane ---
cat >zz_seed_race_test.go <<'EOF'
package webhook

import "testing"

var zzSeedRaceCounter int

func TestZZSeedRace(t *testing.T) {
	done := make(chan struct{})
	go func() {
		zzSeedRaceCounter++
		close(done)
	}()
	zzSeedRaceCounter++
	<-done
}
EOF
red "race (seeded data race)" "zz_seed_race_test.go" lane_race
rm -f zz_seed_race_test.go
# GREEN runs the full lane and writes the profile the floor checks reuse.
green "race" lane_race

# --- tidy: seeded requirement drift trips go mod tidy -diff ---
go mod edit -require=gopkg.in/yaml.v3@v3.0.0
red "tidy (seeded go.mod drift)" "go.mod" lane_tidy
cp "$GOMOD_BACKUP" go.mod
green "tidy" lane_tidy

# --- 500-line cap: a 501-line file trips the wc -l gate; no allowlist ---
{
	echo "package webhook"
	echo ""
	for i in $(seq 1 499); do echo "var _ = $i"; done
} >zz_seed_cap.go
red "cap (seeded 501-line file)" "zz_seed_cap.go" lane_cap
rm -f zz_seed_cap.go
green "cap" lane_cap

# --- coverage floor: a floor above the measured total fails, the real one passes ---
total=$(go tool cover -func="$PROFILE" | awk '/^total:/ {gsub(/%/, "", $3); print $3}')
echo "measured total=${total}%"
floor=$(sed -n 's/^COVERAGE_FLOOR ?= \([0-9.]*\).*/\1/p' Makefile | head -n 1)
ci_floor=$(sed -n 's/^ *coverage-floor: \([0-9.]*\).*/\1/p' .github/workflows/ci.yml | head -n 1)
if [ -z "$floor" ] || [ "$floor" != "$ci_floor" ]; then
	echo "FAIL: floor parity (Makefile='$floor' ci.yml='$ci_floor')"
	fail=$((fail + 1))
else
	raised=$(awk -v t="$total" 'BEGIN { printf "%d", t + 1 }')
	red "floor (raised to $raised, measured $total)" "FAIL: coverage below floor" lane_floor "$raised"
	green "floor at $floor (measured $total)" lane_floor "$floor"
fi

echo "seed matrix: pass=$pass fail=$fail"
if ! diff -u "$SNAPSHOT" <(git status --porcelain) >"$TMP/status-diff.txt" 2>&1; then
	echo "FAIL: tree changed by the matrix run:"
	cat "$TMP/status-diff.txt"
	exit 1
fi
[ "$fail" -eq 0 ]
