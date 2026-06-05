# Config hot reload

## Status
Accepted.

## Context
Operators add/remove PPDM servers and tune intervals without wanting to restart the
exporter (which would drop `/metrics` and re-login everywhere). Family precedent: `ppdd` 0005.

## Decision
A thread-safe `Watcher` reloads and revalidates the config on **`SIGHUP` or file change**
(fsnotify). It watches the parent directory, not the file inode, so editor/templating
temp-file-rename writes still fire. A successful reload emits a validated `*Config`; a bad
reload is logged and dropped (the running config stays). `main` rebuilds clients + collector
on each update (**rebuild-and-swap**), writing into the same `SnapshotStore`; the HTTP server
and Prometheus registry are untouched.

## Consequences
Zero-downtime reconfiguration. A malformed edit cannot take the exporter down. Secrets are
re-interpolated (`${ENV}` / `passwordFile`) on each reload.
