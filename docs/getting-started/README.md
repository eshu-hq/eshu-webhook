# Getting started

You'll have this running locally in a few minutes. It reads
environment variables only, so there's no config file to hunt
for.

## What you need first

- At least one provider secret or freshness token.
- The intake URL and token, set together. Without both the
  process refuses to start, because it would have nowhere to
  hand deliveries.

GitHub only, talking to a core on the same network:

```bash
export ESHU_WEBHOOK_GITHUB_SECRET='the secret you set in GitHub'
export ESHU_WEBHOOK_INTAKE_URL='http://eshu-core:8080'
export ESHU_WEBHOOK_INTAKE_TOKEN='the token core expects'
go run ./cmd/webhook
curl http://127.0.0.1:8080/healthz   # this process is up
curl http://127.0.0.1:8080/readyz    # core answers too
```

It listens on `:8080` by default. `/readyz` also probes core,
so a copy that can't reach core won't take traffic. That's on
purpose.

## Next steps

- Add another provider by setting its secret or token and
  pointing that provider at the matching path; see
  [configuration](../reference/configuration.md).
- Read the [delivery contract](../concepts/contracts.md) for
  which events become triggers.
- See [provider setup](../guides/seed.md) for pointing
  providers at this service. There's no seed step here; that
  guide explains what stands in its place.
