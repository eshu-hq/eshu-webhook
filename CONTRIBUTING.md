# Contributing

## Local checks

`make ci` runs the same `lint`, `test`, and `race` commands CI runs
(`go vet`, `golangci-lint run`, format check, `go mod tidy -diff`, the
500-line cap on `.go` files, `go build`, `go test`, and the race lane
with the coverage floor). Keep it green before pushing. CI adds `shell`,
`image`, `govulncheck`, `gosec`, and `actionlint`; `ci-complete` is the
only required context on `main`.

## Coverage floor

The `race` lane fails below `COVERAGE_FLOOR` (set in the Makefile and
the workflow). New behavior needs tests; the floor may only rise.
