# Documentation

Welcome in. This is the map for `eshu-webhook`, the small HTTP
service that verifies provider webhook deliveries, rewrites them
into Eshu core's intake shape, and hands them over. It stores
nothing and never writes facts; core records a trigger from what
it accepts.

If you're new, start with getting started, then dip into the
concepts. If you're on call, the operate and troubleshoot guides
are where the 3 AM answers live.

| Start here | What's in it |
|---|---|
| [Getting started](getting-started/README.md) | Run it locally and see health answer |
| [Guides](guides/README.md) | Operate, troubleshoot, provider setup, and release |
| [Concepts](concepts/README.md) | The delivery contract and what this service never touches |
| [Reference](reference/README.md) | Every environment variable and signature check |
| [Architecture](architecture/README.md) | Where this service sits in Eshu |
| [Diagrams](diagrams/README.md) | Every diagram, with captions and text versions |
| [Decisions](decisions/README.md) | Architecture decision records |
| [Contributing](contributing/README.md) | Local checks before you open a pull request |

A delivery is one webhook HTTP request. Intake is core's API
that receives checked deliveries. A trigger is the record core
makes after accepting one. The [top-level README](../README.md)
keeps the short version and one overview diagram.
