# Identity retirement validation / review handoff

Base: `10edc7a1731cfef8dbc4622b4319b33b73ae66c5`, draft PR #2.

RED: copied the independently inspected delete-poison reproduction from scratch;
`TestIndependentDeletedCounterMustNotPoisonCollector` failed with zero statistics
rows for `review`, retained pending=true. Subsequent RED slices: whole-inbound
and depleted deletion had no owner; pending rename was globally refused; old
tag after edit was unknown; repeated inbound deletion returned not-found; failed
traffic writes incorrectly published online-client state before commit.

GREEN evidence on the resulting patch:
- `go test -vet=off ./...` passes.
- `go test -vet=off -race ./web/service ./web/controller` passes.
- `go build -o /root/.hermes/cache/scratch/xpanel-retirement .` passes.
- Both Node monthly reset and traffic-identity UI tests pass.
- Real loopback StatsService QueryStats(reset=true) returns old7/new3 followed
  by new4 and an empty poll; retired allTime=7, healthy allTime=7, healthy monthly
  usage reset to zero and cycle marker advances. These are **injected counters**,
  not proxy-generated traffic. Loopback HandlerService failure also exercised.
- SQLite write and deferred-FK commit failures leave deletion/config/archive
  unchanged and issue no core calls; late archive delta failure retains pending;
  retry drains no new batch and applies old7/new3 exactly once. RemoveUser failure
  requests restart with committed data authoritative, without replaying deltas.
- DelInbound, depleted cleanup, bulk client removal, tag change, live and pending
  rename, rename chain, SQLite reopen, email/tag reuse reservations, repeat delete
  and online-state rollback tested.
- Actual controller HTTP response returns success=false with actionable reserved
  identity text and unchanged DB. Node and real Chromium with repository Vue and
  HttpUtil show error toast, restore loading and retain unsaved editor input;
  Chromium reports no page errors. This is a component harness, not a full
  authenticated panel editor end-to-end session.

Scratch evidence: `xpanel-retirement-acceptance.log`,
`xpanel-retirement-race.log`, `xpanel-retirement-browser.py` under
`/root/.hermes/cache/scratch/`.

Plain `go test ./...` still fails only at pre-existing tgbot.go:3675 and
server.go:1375 vet errors. No unrelated baseline cleanup performed.

Limits / review priorities:
- Ownership archives are persistent, but destructive drained pending data is
  still memory-only; crash before commit can lose it. Not crash-durable delivery.
- No archive GC or identity reuse without a verified core-generation fence;
  reservation persists intentionally and archives grow with removed identities.
- Truly unknown identities are integrity errors retained for investigation.
- Existing startup migration/manual DB import paths are not a live identity API.
  Independent review should audit those boundaries and archived statistics privacy,
  plus query overhead for large panels before accepting this design.
- No main merge, release, production DB or deployment touched.
