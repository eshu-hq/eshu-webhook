# Contributing

Thanks for helping. Read [CONTRIBUTING.md](../../CONTRIBUTING.md)
before opening a pull request; this page is the short
version so you can get your local checks green first.

Go version is pinned in `go.mod` (`go 1.26.10`, module
`github.com/eshu-hq/eshu-webhook`, standard library only).

```bash
make ci     # lint + test + race, with the coverage floor
make lint   # vet, golangci-lint, format check, tidy, 500-line cap
make test   # build + full test suite
make race   # race suite + coverage floor (90.0)
```

`make ci` needs `go`, `golangci-lint` v2.12.2, and `git`.
CI adds shell, image, boundary, govulncheck, gosec, and
actionlint lanes. GCP retry and normalization cases are
pinned by recorded fixtures under `testdata/gcp/`; look at
the tests beside `gcp_normalize.go` before touching that
path.

Report security problems as described in
[SECURITY.md](../../SECURITY.md), not as a public issue.
Source files carry `SPDX-License-Identifier: MIT`; there's
no separate LICENSE file in this tree.
