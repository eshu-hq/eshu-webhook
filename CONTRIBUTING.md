# Contributing

## Local checks

`make ci` runs the same `lint`, `test`, and `race` commands CI runs
(`go vet`, `golangci-lint run`, format check, `go mod tidy -diff`, the
500-line cap on `.go` files, `go build`, `go test`, and the race lane
with the coverage floor). Keep it green before pushing. CI adds `shell`,
`image`, `boundary`, `govulncheck`, `gosec`, and `actionlint`. The lane
bodies run from the pinned `eshu-hq/.github` reusable (`go-collector-ci.yml`
at a commit SHA) through the `lanes` job, so check runs read
`lanes / lint`, `lanes / race`, and so on. `ci-complete` is a caller job
and the only required context on `main`; `.github/workflows/ci.yml` lists
the accepted deltas of the move. `ci_workflow_test.go` freezes the pin,
the run gates, the tool versions, and the aggregator.

`bash scripts/webhook-seed-matrix.sh` is a local runner (not a CI gate) that
seeds a defect per lane (vet, lint, format, race, tidy, the 500-line cap, the
coverage floor), asserts RED then GREEN, and leaves the tree as it found it.

## Coverage floor

The `race` lane fails below `COVERAGE_FLOOR` (set in the Makefile and
the workflow). New behavior needs tests; the floor may only rise.
