# X-Panel-WH community downstream

A small downstream of the free [xeefei/X-Panel](https://github.com/xeefei/X-Panel) source, under [GPL v3](LICENSE). Not an official upstream release or Pro product; no proprietary activation bypass.

The maintained entry point is the [Chinese README](README.md), with [documentation](docs/README.md), [build/install/rollback boundaries](docs/build-install-upgrade.md), [compatibility limits](docs/compatibility.md), and [legal credits](docs/credits.md).

Integrated downstream changes: opt-in independent monthly traffic reset per client, removal of the automatic homepage Pro promotion, and removal of inherited hardcoded third-party Telegram credentials/reporting paths (not user-configured notifications). This does not purge Git history or establish credential revocation.

`main` now integrates upstream free `v26.9.13`, downstream features and documentation; PRs #1, #2 and #3 are merged. This repository is the future downstream maintenance/release source. Upstream updates go through integration, regressions, independent review and isolated acceptance before main; releases require separate approval. Planned downstream tags such as `v26.9.13-wh.1` are examples only, not existing releases. Inherited release/Docker workflows are manual-only; merges and tags cannot publish automatically. Documented candidate: `f4f351adef059040cab935492640920e947ba186`. No downstream GitHub Release or public one-click installer exists at the documentation audit. Local packages and controlled deployments are not public releases. Do not use inherited upstream auto-update paths to retain downstream changes.

Accounting is not crash-durable across destructive core reads and SQLite commits. Unknown nonzero identities fail closed; archived identities remain reserved; core reconciliation is asynchronous and may fail. Back up matching binaries, configuration and a consistent database before separately approved upgrades.
