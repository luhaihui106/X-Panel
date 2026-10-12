# Downstream monthly client traffic reset

This is a small GPL-3.0 downstream modification of the **free** source from
[xeefei/X-Panel](https://github.com/xeefei/X-Panel), baseline release `v26.9.13`,
commit `8f6bcd84abe74c9374ae929d28df092e0acd75f3`. The upstream LICENSE and
copyright notices remain unchanged. No Pro binaries, authorization checks,
or proprietary source are used or bypassed.

## Behavior

- Each VMess/VLESS/Trojan/Shadowsocks client has `monthlyResetEnabled` (default
  false) and integer `monthlyResetDay` (1–31 when enabled).
- Uses Panel Settings → timeLocation, including `Local` if configured. Set an
  explicit IANA zone, e.g. `Asia/Shanghai`, to avoid host-zone ambiguity.
- Midnight on the selected day; short months use the month's final day.
- Initial enable or a changed date enrolls in the next cycle, not retroactively.
- On recovery after downtime only the most recent missed boundary resets once.
  SQLite `client_traffics.monthly_reset_day/monthly_reset_at` store enrollment
  and last completed boundary; an ordinary edit preserves the marker.
- Only Up/Down reset. AllTime, limit, identity, expiry and original `reset`
  expiry-renewal semantics are unchanged. Expiry renewal runs independently.
- Quota-disabled traffic can be re-enabled only if the client configuration is
  enabled and unexpired, and pre-reset usage met its quota. Manual-disabled or
  expired configurations are never enabled by this feature.
- Runs in the existing 10-second Xray traffic job, **after GetTraffic(true)**
  drains counters and AddTraffic applies them. Reset and marker share the same
  SQLite transaction. If Xray is stopped/unreachable, reset waits for a successful
  drain; no unsafe zeroing while old core counters remain.
- Older API callers omitting both fields on edits retain existing values;
  explicit false disables. Old exports without fields remain disabled. The
  private cycle marker is not accepted from client JSON; DB backups preserve it.

## Development / update boundary

`integration/upstream-v26.9.13` is untouched upstream source, separate from
`feat/client-monthly-reset`. Review/merge upstream synchronization independently.
Do not merge, tag or publish the feature before independent review and staging.
No production SSH, database or installed panel was accessed during development.

For future updates: fetch upstream, create an integration branch, audit **all**
workflows/scripts, run baseline tests/build, then rebase/cherry-pick the downstream
commit and re-run tests plus isolated API/browser checks. Avoid general formatting
or unrelated refactors in upstream files.

Upstream release.yml runs on main push (build/artifacts), manual dispatch and
published releases (release assets). docker.yml publishes on version tags or
manual dispatch and still targets upstream image namespaces. Neither runs on
these feature/integration pushes. downstream-validation.yml uses read-only
permissions and only tests/builds; it does not upload releases or deploy.
Do not dispatch inherited publishing workflows or push version tags.

The upstream installer can select free/Pro and upstream update scripts can
replace downstream binaries. Do **not** use upstream auto-update to retain this
feature. After approval, install only reviewed **free downstream** assets from
https://github.com/luhaihui106/X-Panel/releases, with a DB backup and verified
source commit/checksum. No release exists for this draft; installation scripts
have intentionally not been broadly rewritten or executed.

## Reproducible verification

Go 1.27, CGO + SQLite toolchain; Node 22:

```sh
go test -vet=off ./...
go test -vet=off -race ./web/service -run TestMonthly -v
node web/assets/js/model/monthly-reset.test.cjs
go build -o ./x-panel .
```

Baseline `go build` passes. Unmodified upstream `go test ./...` / `go vet ./...`
fail at `web/service/tgbot.go:3675` (function comparison) and
`web/service/server.go:1375` (nonconstant fmt.Errorf). Tests with `-vet=off`
pass; these unrelated baseline errors are deliberately not hidden as clean vet.

Local RED evidence was captured before each implementation slice: boundary,
SQLite reset, validation, traffic pipeline, enrollment/save, legacy edit; also
frontend roundtrip failed on absent fields. Isolated live HTTP add/update/get
rejected day 32 and persisted day 29. A real Chromium editor then changed 29→30
and API read-back verified persistence without page errors. The initial strict
legacy decode rejected UI tgId strings; a RED regression test and scoped new-field
validation corrected that without changing upstream legacy decoding semantics.

## Review handoff / limitations

Independent review is mandatory before merge; implementer self-checks are not
independent approval. Review transaction commit-error propagation inherited from
AddTraffic, overlapping traffic-job invocations, cycle persistence after process
restart, original expiry-renewal interaction, email/ID change preservation, and
large-panel query costs. Tests inject drained core deltas; no real proxy client
traffic was generated, and no end-to-end accelerated calendar/core recovery
simulation was performed. Linux arm64 build/API/browser were exercised; other
platform release matrices were not.
