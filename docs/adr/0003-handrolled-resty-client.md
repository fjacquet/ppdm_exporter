# Hand-rolled `resty/v2` client (no Go SDK)

## Status
Accepted.

## Context
The family rule: use the official vendor Go SDK if (1) available and (2) useful; otherwise
hand-roll a lean `resty/v2` client and record why.

For PPDM there is **no official Dell Go SDK**. The official repo
[`dell/powerprotect-data-manager`](https://github.com/dell/powerprotect-data-manager) is
Apache-2.0 *automation enablers* — a PowerShell module and Python scripts — not a Go
library. This fails criterion (1) "available". The sibling data-protection backends `ppdd`
(PowerProtect DD) and `nbu` (NetBackup) are hand-rolled for the same reason.

## Decision
Hand-roll a lean `github.com/go-resty/resty/v2` client (`internal/ppdmclient`): bearer
login, expiry-aware re-login + relogin-on-401, a generic `GetAll[T]` paginator over the
`{page,content}` envelope, and a `Client` interface with an in-memory `Mock` for tests.

## Consequences
Full control over auth, pagination, and the snapshot-friendly batched list calls, with a
minimal dependency tree. The official PowerShell module is retained as a **cross-reference**
for field names and the OData filter syntax (see ADR-0009).
