# systemd (EL9 host)

Docker is **not** required — the exporter is a single static (`CGO_ENABLED=0`) binary. For a
non-container deployment on Enterprise Linux 9, use the unit shipped in `deploy/`.

## Install

```bash
# user + binary
sudo useradd --system --no-create-home --shell /usr/sbin/nologin ppdm
sudo install -m 0755 bin/ppdm_exporter /usr/local/bin/ppdm_exporter

# config + secrets
sudo install -d -o root -g ppdm -m 0750 /etc/ppdm_exporter
sudo install -m 0640 -o root -g ppdm config.yaml /etc/ppdm_exporter/config.yaml
sudo install -m 0600 -o root -g ppdm deploy/ppdm_exporter.env.example /etc/ppdm_exporter/ppdm_exporter.env
# edit /etc/ppdm_exporter/ppdm_exporter.env to set PPDM1_PASSWORD=...

# service
sudo install -m 0644 deploy/ppdm_exporter.service /etc/systemd/system/ppdm_exporter.service
sudo systemctl daemon-reload
sudo systemctl enable --now ppdm_exporter
```

Set `logName: ""` in `config.yaml` so logs go to the journal.

## Operate

```bash
journalctl -u ppdm_exporter -f         # follow logs
sudo systemctl reload ppdm_exporter    # live config reload (sends SIGHUP)
sudo systemctl status ppdm_exporter
```

## Hardening

The unit runs as the unprivileged `ppdm` user inside a sandbox:

- `NoNewPrivileges=true`, `ProtectSystem=strict`, `ProtectHome=true`
- `PrivateTmp`, `PrivateDevices`, `ProtectKernel*`, `ProtectControlGroups`
- `RestrictAddressFamilies=AF_INET AF_INET6`, `RestrictNamespaces`, `LockPersonality`
- `Restart=on-failure`

Secrets are supplied through the `EnvironmentFile` and referenced as `${PPDM1_PASSWORD}`
in `config.yaml`. Keep that file mode `0600`.

## macOS (launchd / Homebrew)

On macOS run it under **launchd** (the systemd equivalent). `brew services` is not wired up:
the Homebrew cask only installs the binary on your PATH — it defines no service block — so
register a `launchd` job yourself, e.g. `~/Library/LaunchAgents/com.fjacquet.ppdm_exporter.plist`
with `ProgramArguments` `[/opt/homebrew/bin/ppdm_exporter, --config, <path>/config.yaml]` and
`RunAtLoad`/`KeepAlive` set, then `launchctl load` it.
