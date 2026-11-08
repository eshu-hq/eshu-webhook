# Configuration reference: eshu-webhook

Everything comes from the environment. There are no flags and no
config file, and `config.go` is where every name below is read.

Two rules before the table: at least one provider secret or
freshness token is required, and the intake URL and token must be
set together. Break either rule and the process won't start, which
is what you want. A running copy that can't hand work to core would
just swallow deliveries.

## Secrets and tokens

| Variable | What it checks |
|---|---|
| `ESHU_WEBHOOK_GITHUB_SECRET` | GitHub `X-Hub-Signature-256` (HMAC SHA-256) |
| `ESHU_WEBHOOK_GITLAB_TOKEN` | Compared with GitLab `X-Gitlab-Token` |
| `ESHU_WEBHOOK_BITBUCKET_SECRET` | Bitbucket `X-Hub-Signature` (HMAC SHA-256) |
| `ESHU_WEBHOOK_PAGERDUTY_SECRET` | PagerDuty `X-PagerDuty-Signature` (`v1=` HMAC SHA-256) |
| `ESHU_WEBHOOK_JIRA_SECRET` | Jira `X-Hub-Signature` (HMAC SHA-256) |
| `ESHU_AWS_FRESHNESS_TOKEN` | Bearer token on AWS EventBridge deliveries |
| `ESHU_GCP_FRESHNESS_TOKEN` | Bearer token on GCP Pub/Sub pushes |
| `ESHU_WEBHOOK_INTAKE_TOKEN` | Bearer token we send to core |

HMAC is a keyed hash: a short code computed from the body and a
shared secret. Matching codes prove the sender knew the secret.
GitLab, AWS, and GCP compare shared tokens instead. Every comparison
uses a timing-safe compare in `signature.go`, and GitHub's legacy
SHA-1 signature header isn't accepted. If you're rotating a secret,
rotate it at the provider and here together. We don't carry old and
new side by side.

Treat these like any other production secret: inject them from your
secret store at runtime, don't bake them into an image, and don't
log them. The service itself never logs a body, secret, token, or
signature.

## Where things listen and where they go

| Variable | Default | Notes |
|---|---|---|
| `ESHU_WEBHOOK_INTAKE_URL` | none | Core intake base URL, scheme and host only |
| `ESHU_WEBHOOK_LISTEN_ADDR` | `:8080` | Listen address |
| `ESHU_WEBHOOK_MAX_BODY_BYTES` | `1048576` | Largest accepted delivery body |
| `ESHU_WEBHOOK_GITHUB_PATH` | `/webhooks/github` | GitHub route |
| `ESHU_WEBHOOK_GITLAB_PATH` | `/webhooks/gitlab` | GitLab route |
| `ESHU_WEBHOOK_BITBUCKET_PATH` | `/webhooks/bitbucket` | Bitbucket route |
| `ESHU_WEBHOOK_PAGERDUTY_PATH` | `/webhooks/pagerduty` | PagerDuty route |
| `ESHU_WEBHOOK_JIRA_PATH` | `/webhooks/jira` | Jira route |
| `ESHU_AWS_FRESHNESS_PATH` | `/webhooks/aws/eventbridge` | AWS route |
| `ESHU_GCP_FRESHNESS_PATH` | `/webhook/gcp-freshness` | GCP route |

## The incident providers need a scope

PagerDuty and Jira events get stamped with a scope id before they
reach the incident intake. Without one, there's nowhere sensible to
file them.

| Variable | Notes |
|---|---|
| `ESHU_WEBHOOK_PAGERDUTY_SCOPE_ID` | Scope stamped on PagerDuty events |
| `ESHU_WEBHOOK_JIRA_SCOPE_ID` | Scope stamped on Jira events |
| `ESHU_WEBHOOK_DEFAULT_BRANCH` | Fallback branch name when a payload omits it |

## A minimal example

GitHub only, talking to a core on the same network:

```bash
export ESHU_WEBHOOK_GITHUB_SECRET='the secret you set in GitHub'
export ESHU_WEBHOOK_INTAKE_URL='http://eshu-core:8080'
export ESHU_WEBHOOK_INTAKE_TOKEN='the token core expects'
go run ./cmd/webhook
```

Add another provider by setting its secret or token and pointing
that provider at the matching path above. Providers you don't
configure stay off.
