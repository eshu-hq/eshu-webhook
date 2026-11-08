# The delivery contract

Git hosts and other tools send webhooks, which are just HTTP
callbacks saying "something changed." Eshu treats those
callbacks as hints to refresh what it knows. This service
sits at the edge: it proves a delivery really came from the
named provider, rewrites it into core's intake form, and
submits it.

See [diagrams](../diagrams/README.md) for the overview and
the one-delivery sequence, with captions and text versions.

## One delivery, start to finish

Data in is one `POST` per delivery, on a provider path,
carrying the provider's signature or token header. Bodies
over the size cap are rejected. The cap defaults to 1 MiB
(1048576 bytes).

In prose: we read the body within the cap, check the
signature or token (a bad check gets `401` and stops), then
rewrite known events into the intake form. Unknown events
get `422`, and so does a verified delivery we can't rewrite.
We post the form to core with a Bearer [REDACTED] and core's
answer decides the final status: `202` for accepted work,
`409` for a conflicting delivery, `502` when the hand-off
itself fails.

Data out is those intake posts, one JSON log line per
provider request on stderr, and metrics on `GET /metrics`.

## Which events we accept

Only verified, mapped events become triggers. We never write
facts, and anything the code doesn't map is turned away:

- GitHub: `push`; `pull_request` mapped to
  `pull_request_merged` (an unmerged close carries an
  `ignored` decision); `workflow_run` and `workflow_job` go
  to the actions intake. A signed `ping` answers `202`
  locally with reason `ping` and makes no intake call.
- GitLab: `push` and merged merge requests.
- Bitbucket: `repo:push` and `pullrequest:fulfilled`.
- PagerDuty and Jira: incident-style events, sent to the
  incident intake. Each needs its configured scope id (see
  [configuration](../reference/configuration.md)).
- AWS: EventBridge freshness deliveries, checked with a
  Bearer [REDACTED]
- GCP: Pub/Sub push messages for Cloud Asset Inventory
  changes, checked with a Bearer [REDACTED] The
  first-subscription welcome message is answered locally
  (`202`, reason `welcome_message`) and isn't sent to core.

The intake routes, per `intake.go`, are
`/api/v1/webhook/intake/repository`,
`/api/v1/webhook/intake/actions`,
`/api/v1/webhook/intake/incident`,
`/api/v1/webhook/intake/aws`, and
`/api/v1/webhook/intake/gcp`. Every submission carries
intake API version `v1`.

## Which core versions we speak to

The supported core range comes from the wire check recorded
in the prior README: Eshu `main` from `50ba1f2a70`
(2026-10-30, the first build with the `webhook-intake` host)
through `d2eca4fb01` (2026-11-01, the newest build checked).
No tagged core release carried the host at that check. Core
tags ended at `v0.0.3-pre-release-18`. If you change the
wire version, change this range in the same change.
`compatibility_test.go` pins the `v1` string.

For the package-level summary see `doc.go`.
