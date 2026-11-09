# Boundaries

Why split this out at all? Signature checks and provider
quirks should live in one small place, core should receive
one trusted common shape, and keeping the two apart means
this service can run and release on its own.

## What we depend on

We need core's intake API at `ESHU_WEBHOOK_INTAKE_URL` with
the matching Bearer [REDACTED], plus the provider secrets for
whichever providers you turn on. We talk to core (`POST
/api/v1/webhook/intake/*`, plus `GET /healthz` for
readiness) and to the providers that call us.

## What we never touch

That's it. No database, no graph, no queue, no volume. The
code keeps no storage at all. Durable trigger records stay
in core where they belong, and there's deliberately no path
from webhooks to facts in this repo. Collectors do the
actual reading once core decides a refresh is due; nothing
here calls them directly.

See [architecture](../architecture/README.md) for where this
fits in Eshu as a whole.
