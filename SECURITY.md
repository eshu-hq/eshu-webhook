# Security Policy

eshu-webhook is a stateless satellite: it verifies provider webhook
signatures and tokens, normalizes deliveries, and submits them to the Eshu
core intake API. It stores nothing. The security-relevant surface is
signature and token verification, the request body cap, the intake bearer
token it holds, and the container image it ships.

## Supported Versions

Security fixes target the latest tagged version only. No GitHub Release has
been published yet; the only tag today is `v0.1.0`, so until a newer tag
exists that tag is the supported version.

| Version | Supported |
| ------- | --------- |
| Latest tagged version | Yes |
| Older tags | No |

The satellite is also paired with a range of core builds. See
[Supported core versions](README.md#supported-core-versions) in the README.

## Reporting a Vulnerability

Do not open a public GitHub issue for a security problem.

1. Private vulnerability reporting is currently disabled on this repository,
   so the Security tab here has no "Report a vulnerability" button. Use
   private vulnerability reporting on the core repository,
   [eshu-hq/eshu](https://github.com/eshu-hq/eshu/security), and name
   eshu-webhook in the report. That is the live path today.
2. If reporting is enabled on this repository later, it may be used instead
   and this file will be updated.
3. Include the impact, reproduction steps, the affected version or commit,
   and any mitigation you know of.

## What To Expect

We acknowledge a report, confirm or dispute it, and agree a disclosure date
with the reporter. A confirmed issue is fixed on the latest tagged version, shipped as
a new version tag, and described in a GitHub security advisory that names the
fixed version. Reports made in good faith get credit unless the reporter
asks otherwise.

## Scope

In scope: a bypass of signature or token verification, a body that evades the
1 MiB cap, a log line or metric that exposes a delivery body or a credential,
a route that forwards unverified content to core, and a vulnerable dependency
reachable from the shipped binary or image.

Out of scope: flaws in the upstream provider's own webhook delivery, and
findings that need access to the operator's environment variables or the
intake bearer token.

## Continuous Checks

CI runs `govulncheck` and `gosec` on code changes, and Dependabot keeps the
module and the pinned CI actions current. Release images are published with
build provenance, an SBOM, and a cosign signature (see
`.github/workflows/image-publish.yml`).

## Operational Notes

- Keep the webhook secrets and the intake bearer token in a secret store,
  never in the image or the repository.
- Route only the configured webhook paths through a public ingress. `/metrics`,
  `/healthz`, and `/readyz` share the listener and belong inside the cluster.
- Restrict the satellite's egress to the core intake host.
