# Architecture

## Place in Eshu

`eshu-webhook` is an edge service in front of Eshu core. It
doesn't replace the webhook handling in core yet (status:
pre-release). Providers (GitHub, GitLab, Bitbucket,
PagerDuty, Jira, AWS, GCP) send signed deliveries here; this
service verifies and normalizes them, then posts them to
core's intake API. Core records the trigger and owns
everything downstream. Collectors do the reading once core
decides a refresh is due.

The overview and sequence diagrams, with captions and text
versions, live in [diagrams](../diagrams/README.md). The
[top-level README](../../README.md) keeps one overview
diagram for quick reading.

## Shape of the service

- One HTTP listener (default `:8080`) serving provider
  paths, `/healthz`, `/readyz`, and `/metrics`.
- Environment-only configuration (`config.go`); signature
  and token checks in `signature.go`; intake posts in
  `intake.go`.
- Standard library only, Go `1.26.11` pinned in `go.mod`
  (module `github.com/eshu-hq/eshu-webhook`).
- No storage of any kind. See
  [boundaries](../concepts/boundaries.md).

## Related repos

- [eshu](https://github.com/eshu-hq/eshu): core. It owns the
  intake API, the stored triggers, and everything downstream.
- [eshu-collector-aws-cloud](https://github.com/eshu-hq/eshu-collector-aws-cloud)
  and the other collectors: they read sources once core
  decides a refresh is due. Nothing here calls them directly.
