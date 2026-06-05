# Architecture Decision Records

Decisions for `ppdm_exporter`, following the family `exporter-standards`. Each record:
Status / Context / Decision / Consequences.

| ADR | Title |
|---|---|
| [0001](0001-ci-supply-chain-hardening.md) | CI & supply-chain hardening |
| [0002](0002-prometheus-snapshot-model.md) | Snapshot collection model |
| [0003](0003-handrolled-resty-client.md) | Hand-rolled `resty/v2` client (no Go SDK) |
| [0004](0004-bearer-auth-retry-policy.md) | Bearer auth & retry policy |
| [0005](0005-config-hot-reload.md) | Config hot reload |
| [0006](0006-label-key-consistency-invariant.md) | Label-key consistency invariant |
| [0007](0007-metric-naming-and-units.md) | Metric naming & units |
| [0008](0008-serve-http-before-first-collect.md) | Serve HTTP before first collect |
| [0009](0009-provisional-api-mappings.md) | Provisional API mappings & validation |
