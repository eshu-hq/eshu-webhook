# eshu-webhook

Eshu webhook admission and normalization. Pre-release: holds no cutover authority until its reviewed cutover. Refs eshu-hq/eshu#8876, part of eshu-hq/eshu#6707. Scaffolded from the #6710 collector template (eshu-collector-terraform-state).

Stateless verifier/normalizer satellite: verifies provider webhook
signatures, normalizes deliveries, and submits them to core's versioned
intake API (`POST /api/v1/webhook/intake/*`, bearer auth, 1 MiB bounds).
It performs zero storage — migrations 238/239 and durable trigger truth
stay core-side (eshu-hq/eshu#9377).

## Run

Configure via environment (at least one provider secret plus the intake
uplink), then `go run ./cmd/webhook`:

| Variable | Purpose |
|---|---|
| `ESHU_WEBHOOK_GITHUB_SECRET` | GitHub `X-Hub-Signature-256` secret |
| `ESHU_WEBHOOK_GITLAB_TOKEN` | GitLab `X-Gitlab-Token` secret |
| `ESHU_WEBHOOK_BITBUCKET_SECRET` | Bitbucket `X-Hub-Signature` secret |
| `ESHU_WEBHOOK_PAGERDUTY_SECRET` | PagerDuty signature secret |
| `ESHU_WEBHOOK_JIRA_SECRET` | Jira signature secret |
| `ESHU_AWS_FRESHNESS_TOKEN` | EventBridge bearer token |
| `ESHU_GCP_FRESHNESS_TOKEN` | Pub/Sub push bearer token |
| `ESHU_WEBHOOK_INTAKE_URL` | Core intake base URL (required with token) |
| `ESHU_WEBHOOK_INTAKE_TOKEN` | Core intake bearer token (required with URL) |
| `ESHU_WEBHOOK_MAX_BODY_BYTES` | Delivery cap, default 1048576 (1 MiB) |
| `ESHU_WEBHOOK_LISTEN_ADDR` | Listen address, default `:8080` |

`GET /healthz` is local liveness; `GET /readyz` probes core's `/healthz`
and reports unready when the uplink is down.

A signed GitHub `ping` (sent when a webhook is created or redelivered)
answers `202` with `{"status":"ignored","decision":"ignored","reason":"ping"}`
and makes no intake call. An unsigned ping is `401`; any other unsupported
GitHub event is `422`.

## Telemetry

Every provider request writes one JSON log record to stderr and updates two
Prometheus series served on `GET /metrics` (text exposition 0.0.4, same
listener as the webhook routes). `/healthz`, `/readyz`, and `/metrics`
themselves are not counted.

Log record `webhook request` fields: `route`, `provider`, `method`, `status`,
`outcome`, `reason`, `delivery_id`, `duration_ms`. Level is INFO for
`accepted` and `ignored`, WARN for `rejected`, ERROR for `failed`. A log line never carries
the body, a secret, a token, or a signature; `delivery_id` is the provider
header (at most 200 bytes, cut on a rune boundary) or the `sha256:` content hash the intake
submission also uses.

| Series | Type | Labels |
|---|---|---|
| `eshu_webhook_requests_total` | counter | `provider`, `outcome`, `reason` |
| `eshu_webhook_request_duration_seconds` | histogram | `provider` |

`provider` is one of `github`, `gitlab`, `bitbucket`, `pagerduty`, `jira`,
`aws`, `gcp`. `outcome` is `accepted` (2xx), `ignored` (a verified delivery answered
without an intake call), `rejected` (4xx), or `failed` (5xx). `reason` is a closed machine vocabulary, never request content:

| `reason` | Status | Meaning |
|---|---|---|
| `accepted` | 202 | verified, normalized, and handed to core |
| `ping` | 202 | signed GitHub ping answered locally (outcome `ignored`); no intake call |
| `bad_signature` | 401 | HMAC signature missing or wrong (GitHub, Bitbucket, PagerDuty, Jira) |
| `bad_token` | 401 | static token wrong (GitLab, AWS, GCP) |
| `oversized_body` | 413 | body over `ESHU_WEBHOOK_MAX_BODY_BYTES` |
| `unreadable_body` | 400 | the request body could not be read |
| `method_not_allowed` | 405 | not a POST |
| `unsupported_event` | 422 | verified delivery of an event the satellite does not map |
| `invalid_payload` | 422 | verified delivery that failed normalization |
| `delivery_conflict` | 409 | core reports a conflicting delivery identity |
| `intake_failed` | 502 | core intake unreachable or errored; GitHub does not retry automatically, so the failed delivery shows in GitHub's delivery log and needs a manual redelivery within 3 days |
| `panic` | 500 | handler panic |
| `other` | any | fallback for a status that maps to none of the reasons above |

Server wiring: call `WithLogger` before serving starts. It sets a field
without synchronization and is not safe to call while requests are in
flight. The instrumentation wraps the response writer in a recorder that
exposes only `Write` and `WriteHeader`, so a handler cannot reach
`http.Flusher` or `Unwrap` through it; the webhook handlers write one small
JSON body and need neither.

The `eshu_webhook_` namespace is deliberate: the core intake host emits its
own `eshu_dp_webhook_*` series for a different hop, and the two must not
collide in one Prometheus. The satellite has no metrics client library
because the import boundary admits only the standard library, so the
exposition is written directly.

Example scrape (pod annotations):

```yaml
prometheus.io/scrape: "true"
prometheus.io/port: "8080"
prometheus.io/path: "/metrics"
```

Or a `ServiceMonitor` endpoint on the container port that serves
`ESHU_WEBHOOK_LISTEN_ADDR`:

```yaml
endpoints:
  - port: http
    path: /metrics
    interval: 30s
```

`/metrics` shares the webhook listener, so route only the configured webhook
paths through any public ingress and scrape `/metrics` in-cluster.

## Supported core versions

The satellite and core ship separately. Core owns the intake API; the
satellite emits one wire version of it.

- Intake API version: `v1` (`api_version` on every submission; core accepts
  absent or `v1` and rejects anything else with `unsupported_api_version`).
  `compatibility_test.go` asserts this exact string on the wire for every
  intake family, so changing it fails a test before it fails in production.
- Oldest supported core build: Eshu `main` at `50ba1f2a70` (2026-10-30), the
  first commit that carries the `webhook-intake` host serving the v1 API. The
  v1 intake API package itself first appeared at `39289ff019` (2026-10-28),
  which has no standalone host.
- Newest supported core build: Eshu `main` at `d2eca4fb01` (2026-11-01), the
  newest build checked. The v1 wire code in
  `go/internal/webhook/intake/api.go` and the `webhook-intake` host are
  unchanged on the wire since `39289ff019` (the only non-comment change is an
  import path). Raise this line when a newer core build is checked.
- No tagged core release carries the intake host yet (core tags end at
  `v0.0.3-pre-release-18`). Until one does, the pair is stated as core
  commits; replace them with release tags once core publishes one.
- This pair is a read of the wire contract and git history, not an end to end
  run against every build in the range. The deployed proof is tracked on
  eshu-hq/eshu#8876.

A change to the intake `api_version` is a breaking change: bump the satellite
and the supported range together, and update this section in the same pull
request.

## Security

See [SECURITY.md](SECURITY.md) for how to report a vulnerability and which
versions get fixes.

## GCP Cloud Asset Inventory pushes

`POST` on the GCP route accepts a Pub/Sub push whose `message.data` is a
base64 Cloud Asset Inventory `TemporalAsset`, the same input core's own push
handler decodes. `kind` is `asset_change`, or `asset_deleted` when the
asset's `deleted` flag is set (falling back to `priorAsset`); the parent
scope comes from `ancestors`, and `observed_at` from `window.startTime`, then
the asset `updateTime`, then the message `publishTime`. `message.attributes`
are not read. The first-subscription welcome message (a bare JSON string)
is acknowledged with `202` (`status: ignored`, `reason: welcome_message`)
and never reaches core, so Pub/Sub does not retry it. Input core would reject
(bad base64, no asset, no parseable ancestors, missing or wildcard asset
type) answers `422` here and is not forwarded.

**GCP parity.** The satellite cannot import core, so
`testdata/gcp/*.push.json` (sanitized recorded pushes) and
`*.want.json` (expected intake requests) are golden fixtures. They were
checked once against core by a throwaway test in a core worktree
(`origin/main` 87eebb09): for every fixture, core's `NormalizePubSubPush`
trigger equals the trigger core's intake builds from the golden request, and
every rejected shape in `gcp_normalize_test.go` is rejected by core too.
Re-run that check when core's `gcpcloud/freshness/pubsub.go` changes.

## Verify

`make ci` runs vet, golangci-lint, tests, and the race lane with the
coverage floor. `docker build -t eshu-webhook .` builds the distroless
nonroot amd64 image. In CI, `ci-complete` is the only required context:
it passes when needed lanes pass and when docs-only changes
legitimately skip every lane.
