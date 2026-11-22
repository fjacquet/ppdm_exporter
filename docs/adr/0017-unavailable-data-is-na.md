# ADR-0017 — Unavailable data is N/A, not zero

Status: Accepted
Date: 2026-11-22

## Context

PPDM 20.3.0 adds protection-rule endpoints (`/api/v3/rules`,
`/api/v3/protection-policy-rule-metrics`). Older servers answer 404. A rule that never ran
has no `lastRunAt`. Showing 0 in either case would read as "healthy" when the truth is
"unknown".

## Decision

- **No series, not a zero.** When data does not exist, the exporter emits no sample.
- **404 means unsupported, not broken.** `ppdmclient.Get` returns a wrapped
  `ErrNotFound` on 404. The `Rules` collector skips that endpoint without error, so
  `ppdm_collector_up` stays 1. Any other error (5xx, network, auth) stays an error
  (`ppdm_collector_up=0`).
- **Dashboard renders N/A.** New panels set `fieldConfig.defaults.noValue: "N/A"`;
  `TestRulePanelsShowNAWhenNoData` enforces it.

## Consequences

- "Collectors degraded" only lights up for real failures.
- Existing panels still show 0 / "No data" for missing data. Aligning them is a follow-up.
- A server that is both too old and broken is indistinguishable from "too old" for the
  rules collector; the other collectors still report the breakage.
