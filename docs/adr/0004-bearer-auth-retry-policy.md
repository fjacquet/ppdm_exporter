# Bearer auth & retry policy

## Status
Accepted.

## Context
PPDM 19.22.0 uses OAuth-style bearer auth. `POST /api/v2/login` with `{username,password}`
returns `{access_token, token_type:"Bearer", expires_in:1800, refresh_token, scope}`.
Subsequent requests send `Authorization: Bearer <access_token>`. The access token lives
30 minutes. Family precedent: `ppdd` 0004.

## Decision
- Cache the access token with its expiry; re-login when within 60s of expiry **and** once
  on any `401`.
- Retries apply to `5xx` only — **never retry `4xx`** (auth/permission failures must not loop).
- TLS floor 1.2; `insecureSkipVerify` is an explicit per-server operator opt-in.
- `refresh_token` is **not** used in v1: re-login is simpler and the 30-min lifetime makes
  it cheap. Adopting the refresh flow is a future optimization.

## Consequences
Robust against token expiry and rotation without leaking retries on bad credentials.
Login response field names are validated; the `refresh_token` path is documented as deferred.
