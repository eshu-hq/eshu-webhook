# Operate eshu-webhook

## The image

```bash
docker build -t eshu-webhook .
```

You get one static binary, `/usr/local/bin/webhook`, on a
distroless base, running as user `65532`. It needs no volumes
and runs no migrations. Give it the environment from
[configuration](../reference/configuration.md) and it listens
on `:8080` unless you say otherwise.

One exposure note: only send the provider webhook paths
through a public entry point. Scrape `/metrics` inside the
cluster, because it shares the same listener as everything
else.

## Health, metrics, and logs

```bash
curl http://127.0.0.1:8080/healthz   # this process is up
curl http://127.0.0.1:8080/readyz    # core /healthz also answers
curl http://127.0.0.1:8080/metrics   # request counts and timing
```

`/readyz` reports not-ready when core can't be reached. A copy
that can't hand work to core shouldn't get traffic, so that's
by design, not a bug to work around.

Metrics come out as Prometheus text `0.0.4`:
`eshu_webhook_requests_total` (counter, labels `provider`,
`outcome`, `reason`) and
`eshu_webhook_request_duration_seconds` (histogram, label
`provider`). Health and metrics paths aren't counted.

Logs are one JSON record per provider request on stderr, with
`route`, `provider`, `method`, `status`, `outcome`, `reason`,
`delivery_id`, and `duration_ms`. Log lines never carry the
body, a secret, a token, or a signature.

The diagrams for this flow live in
[diagrams](../diagrams/README.md). When delivery reasons point
at a fault, go to [troubleshoot](troubleshoot.md).
