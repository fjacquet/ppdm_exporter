# Provisional API mappings & validation

## Status
Accepted (living checklist).

## Context
The exporter was built **without a live PPDM server**. JSON shapes are modeled from the
PPDM 19.22.0 REST API reference (the PDF Dell renders at developer.dell.com; the OpenAPI/
Postman is not directly downloadable) and cross-checked against the Apache-2.0
`dell/powerprotect-data-manager` PowerShell module. Each shape is isolated to one struct +
one fixture, and every unverified field is tagged `// provisional` (`grep -rn provisional`).

## Decision
Maintain a **two-bucket** validation checklist. Validate Bucket B against a live server
before trusting that data; fixing a field = edit one struct + one fixture + rerun one test.

**Bucket A — validated-by-reference** (PDF and/or the Apache-2.0 module):
- auth: `POST /api/v2/login` body / `access_token` / `Authorization: Bearer`.
- pagination: `?pageSize=` + `{content,page}` envelope.
- activities: `state`, `result.status`, OData `filter=… ge "…"`.
- alerts `severity`: `CRITICAL`/`WARNING`/`INFORMATIONAL` (PDF-confirmed).
- assets `type` (confirmed by `assetmgmt.py`).

**Bucket B — still unconfirmed by every source** (live-validate, highest priority):
- capacity (`datadomain-mtrees`, `storage-systems`) field names — ambiguous even in the PDF;
  consider sourcing authoritative DD capacity from `ppdd_exporter`.
- `health-entities` status enum.
- alerts `acknowledgement.acknowledgeState` enum values (field name from the module).
- assets `protectionStatus` enum + `lastAvailableCopyTime` format.
- `activities.result.bytesTransferred` presence across categories.

## Consequences
Correcting a wrong field touches one file. The isolation design (one struct + one fixture
per shape) and the `// provisional` grep target keep the live-validation surface explicit and small.
