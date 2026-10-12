# X-Panel-WH community downstream

A Web management panel for Xray proxy inbounds, clients and traffic. A small downstream of the free [xeefei/X-Panel](https://github.com/xeefei/X-Panel) source, preserving the upstream interface and main functionality. Not an official upstream release or Pro product; no proprietary activation bypass.

[中文](README.md) · [Documentation](docs/README.md) · [Build/install/rollback](docs/build-install-upgrade.md) · [Legal credits](docs/credits.md)

## What this project can do

This list follows the free source's routes, services and frontend, not Pro placeholders or purchase prompts.

- **Manage proxy inbounds:** create, edit, enable/disable and delete VLESS, VMess, Trojan and Shadowsocks (SS) inbounds; configure ports, transports and applicable TLS/Reality options. Available combinations depend on the protocol and Xray core.
- **Manage individual clients:** set separate traffic quotas, expiry and enabled/disabled state; add, edit, delete and manually reset client traffic. SS multi-user support depends on the cipher.
- **View traffic and activity:** inspect inbound/client upload and download (Up/Down), cumulative usage (AllTime), online clients and last activity. Online status is derived from core traffic collection, not a guarantee of exact device or connection counts.
- **Share connections and migrate configuration:** generate connection links and QR codes; provide client subscription links and JSON subscriptions when the subscription service is enabled; import/export inbound configuration JSON for use with compatible clients.
- **Monitor and manage the system/core:** view CPU, memory, disk, network and Xray status, inspect logs, stop/restart the core, and edit panel settings and Xray configuration.
- **Local backups and optional notifications:** download/import the panel database and export Xray configuration; retain user-configured Telegram notifications and database backup delivery. Bot credentials, recipients and network access must be configured; not every Telegram menu is available in the free edition.

**Not included:** Pro master/slave or remote multi-server management, remote backup/restore, or Telegram one-click configuration. Their UI entries or restriction prompts may remain; this downstream does not unlock them.

## Downstream improvements

- **Independent monthly reset per client:** opt in separately, choose a day from **1–31**, **disabled by default**. Uses the **panel timezone** and the month's last day in short months. Only period Up/Down resets; **AllTime is preserved**, along with quota and expiry rules. It does not enable manually disabled or expired clients. [Semantics and limits](docs/downstream-monthly-reset.md)
- **No automatic homepage advertisement:** removes the automatic Pro welcome promotion, not every manually opened feature-restriction prompt. [Scope](docs/home-promotion-removal.md)
- **No inherited hardcoded reporting:** removes third-party Telegram credentials and reporting paths not configured by the user; **user-configured notifications remain**. This does not purge history, prove credential revocation or constitute a complete security audit.

## Using monthly client resets

1. Confirm Panel Settings → timezone (`timeLocation`), preferably an explicit IANA zone such as `Asia/Shanghai`.
2. Open the client editor from the inbound list.
3. Enable monthly traffic reset, enter a day (1–31), and save. Clients can use different days; leave it off where not needed.
4. Enrollment starts with the next cycle. The core traffic job processes the boundary after midnight in the panel timezone; day 31 falls on the last day of February. Enabling/changing the date does not retroactively reset an old cycle. If the core is stopped/unreachable, processing waits for recovery.

Quota and expiry still apply independently; monthly reset does not extend validity. See the [monthly-reset documentation](docs/downstream-monthly-reset.md) for API compatibility, downtime recovery and accounting limits.

## Getting and installing

`main` integrates upstream free `v26.9.13` and these downstream changes. **No downstream GitHub Release or public one-click installer currently exists.** Start with the [pinned-source build instructions](docs/build-install-upgrade.md); full operation also requires matching Xray and data assets.

Inherited `install.sh` and `x-ui.sh` still use upstream download/update paths and free/Pro branching. **Do not use them as production upgrades that preserve downstream changes.** Back up consistent databases, configuration and binaries, validate in isolation, and restore matching old binaries/database snapshots for rollback. [Installation and rollback](docs/build-install-upgrade.md) · [Platform evidence and compatibility](docs/compatibility.md)

## Safety and attribution

Accounting is not atomic across Xray reads and SQLite commits: a crash can lose unpersisted deltas. Unknown traffic identities can block collection/resets; core recovery may fail. [Limits](docs/compatibility.md) · [Identity archives](docs/traffic-identity-retirement.md). Do not publish production databases, credentials, subscription links or machine details.

Licensed under [GPL v3](LICENSE), retaining upstream copyright and attribution; see [credits](docs/credits.md) for X-UI, 3X-UI, X-Panel and third-party components. No claim of original authorship or ownership of all third-party rights. Maintenance, candidate identity and release boundaries are documented [here](docs/build-install-upgrade.md#维护与发行状态).
