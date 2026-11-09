# Troubleshoot eshu-webhook

Start with `/readyz`. If providers report failures and
`/readyz` fails too, core or the network path to core is your
problem, not the signatures. That's the most common 3 AM
answer.

If `/readyz` is fine, read the `reason` field in the logs:

- `bad_signature` or `bad_token`: the secret here and the
  secret at the provider disagree. Check which one changed
  last.
- `oversized_body`: raise `ESHU_WEBHOOK_MAX_BODY_BYTES`, or
  find out why deliveries grew.
- `unsupported_event`: the provider sent an event we don't
  map. See the list in [contracts](../concepts/contracts.md).
- `delivery_conflict` (`409`): core already has this
  delivery. Usually a retry of one that actually landed.
- `intake_failed` (`502`): the hand-off to core failed. Note
  that GitHub won't retry a failed delivery on its own, so a
  `502` has to be redelivered by hand from GitHub's delivery
  log.

Reasons worth knowing also include `accepted`, `ping`,
`invalid_payload`. Startup failures almost always mean
configuration: no provider secret or token set, or only one
of the intake URL and token. The process refuses to start
rather than run half-configured. Double-check names in
[configuration](../reference/configuration.md).
