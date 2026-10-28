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

## Verify

`make ci` runs vet, golangci-lint, tests, and the race lane with the
coverage floor. `docker build -t eshu-webhook .` builds the distroless
nonroot amd64 image.
