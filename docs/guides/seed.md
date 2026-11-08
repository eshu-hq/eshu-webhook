# Provider setup (there is no seed step)

Let's save you a search: this repo has no `seed` command and
no install step. It's a plain HTTP service. "Seeding" here
means pointing each provider at the right path with the right
secret, and the table in
[configuration](../reference/configuration.md) lists every
route and variable.

The short version:

1. Set the provider's secret or freshness token here (for
   example `ESHU_WEBHOOK_GITHUB_SECRET`). Providers you don't
   configure stay off.
2. In the provider, create a webhook pointing at this
   service's matching path (for example `/webhooks/github`)
   with the same secret, so signatures line up.
3. Confirm `/readyz` passes, so deliveries have somewhere to
   go. See [operate](operate.md).

PagerDuty and Jira also need their scope ids
(`ESHU_WEBHOOK_PAGERDUTY_SCOPE_ID`,
`ESHU_WEBHOOK_JIRA_SCOPE_ID`) before their events can be
filed at the incident intake. Rotating a secret? Rotate it
at the provider and here together; we don't carry old and new
side by side.
