# Release eshu-webhook

Pushes to `main` publish `:main`, `:latest`, and
`:sha-<full-sha>` images to GHCR, and version-tag pushes
publish that tag, all through
`.github/workflows/image-publish.yml`. Pin by digest in
anything you'd rather not have move under you.

If a release changes the intake wire version, the supported
core range in [contracts](../concepts/contracts.md) has to
move in the same change. That range currently reads: Eshu
`main` from `50ba1f2a70` (2026-10-30, the first build with
the `webhook-intake` host) through `d2eca4fb01` (2026-11-01,
the newest build checked). No tagged core release carried
the host at that check; core tags ended at
`v0.0.3-pre-release-18`. `compatibility_test.go` pins the
`v1` string.

Run `make ci` before you tag; see
[contributing](../contributing/README.md).
