# Stage 1 Telegram security handoff

Base: PR #8, 9f0ba9ca6be3fff9572db3bf7a1be60082714a02. Existing entertainment/promotion cleanup retained.

## Implemented
- One fail-closed callback gate before both dispatch switches; actor rechecked from CallbackQuery.From.ID, caller isAdmin flag ignored.
- Private chat must match actor. Commands and wizard input also enforce this recipient boundary.
- Random issued button tokens bound to actor, Telegram message and 20-minute lifetime. Successful edits replace previous token generations; old plain callbacks, expired, unknown, absent buttons and duplicate deliveries denied. Mutation tokens one-shot; fresh renders permit legitimate retries/navigation.
- Named ordinary usage verifies exact client TgID; existing ordinary usage query remains TgID-filtered.
- Shared add-client draft is serialized with one owner lease and latest wizard message; other administrator cannot take over. Full per-administrator drafts remain a later refactor, not a claim of concurrent multi-admin support.
- Separate pending UsersShared request generations; close/removal invalidate requests, including outstanding requests for the removed client across administrators.
- Failed-login notification no longer accepts a password; controller no longer logs or forwards it. Username/IP/time/admin destinations retained.
- Concise monthly help with detailed tooltip, no accounting/scheduling change.

## Executed evidence
- RED: real local Telegram API handler unauthorized backup reached side-effect path (panic), and no deny answer. GREEN: denies only answerCallbackQuery for issued ordinary-user dangerous callbacks; no services attached, therefore any dispatch side effect fails test.
- RED password-source contract; GREEN no password notification argument/emission or failed-login log forwarding.
- Real isolated SQLite named query ownership: other denied, own allowed; traffic unchanged.
- Callback gate admin dry authorization, replay, unknown/group/expired/removed keyboard, old generation, refresh and two-admin wizard checks.
- go test -vet=off ./... PASS; go test -vet=off -race ./web/service PASS; go build PASS (Go 1.27 linux/arm64).
- Node monthly-reset, traffic-identity, homepage-promotion and concise-monthly-help PASS.
- Real rebuilt authenticated full panel Chromium: short help and tooltip visible, no page errors; isolated panel stopped. Evidence /root/.hermes/cache/scratch/stage1-monthly/evidence.json.
- go vet exposes inherited issues: tgbot.go comparison of function Message == nil; server.go non-constant fmt.Errorf format.

## Review / remaining acceptance limits
Independent read-only Hermes reviews under scratch x-panel-stage1-review*.txt found real regressions; corrected in writer. Final review status must be read before merging. Do NOT declare overall security acceptance merely from unit/race success.
Coverage limitation: administrator destructive handler side effects are not exercised with full dry adapters; tests assert gate allowance without executing backup/reset/install/restart. UsersShared end-to-end update handler and transient duplicate-email retry need independent behavioral acceptance. No production RN5 changes, release, installer remake or merge performed.

## Resume checkpoint (NOT stage-1 acceptance)
Resume base exact SHA: e58910bffd5aaeb2b16c754c3a40ddf1e45be63b. Tested and independently reviewed code checkpoint exact SHA: 5a0a605d2172ed19605e4ba7321e28820ab7cd03. This checkpoint is intentionally incomplete and PR #8 must remain draft. Subsequent report-only commit does not change the tested code.

- Added real loopback Telegram callback denial for every allowlisted action except the three ordinary read actions. Attached isolated SQLite, asserted deny-only API calls and unchanged client traffic/binding. No admin destructive dispatch was run.
- Replaced legacy nonnegative random request IDs with negative IDs from a durable SQLite AUTOINCREMENT ledger. Successful reservation precedes publishing the selection keyboard; storage errors/exhaustion remove pending and fail closed. This prevents legacy-positive collisions and reuse across ordinary database reopen, NOT database rollback/restore.
- Actual RED (after fixing test fixture locale initialization/path): legacy-positive request namespace reused; request escaped failed durable reservation. Output: /root/.hermes/cache/scratch/stage1-binding-red.txt. GREEN: /root/.hermes/cache/scratch/stage1-binding-green.txt. Reopen, insert failure, signed minimum/exhaustion and all ordinary callback denial tests pass. Exhaustion/denial tests initially passed; they are regression coverage, not additional claimed RED cycles.
- Current all-Go -vet=off, service race, build and four Node suites pass. Authenticated rebuilt browser monthly short help/tooltip pass, zero page errors, isolated panel stopped. Logs: /root/.hermes/cache/scratch/stage1-resume-allgo.txt and stage1-resume-race.txt; browser evidence remains stage1-monthly/evidence.json under scratch.
- Inherited vet failures remain separate: server.go nonconstant fmt.Errorf; tgbot.go function Message compared to nil. No vet clean claim.

Independent read-only reviewer completed and exited; full findings: /root/.hermes/cache/scratch/stage1-resume-newdiff-review.txt. Remaining blockers:
1. P1: ImportDB/old-backup restore rolls back the issuance ledger; a previously issued negative ID can reappear and bind a late response to a new client. Need an issuance high-water mark outside the replaceable business DB or equivalent trusted generation; test actual restore scenario before acceptance. Do not describe this checkpoint as collision-safe overall.
2. Storage failure test lacks outbound request_users/ReplyKeyboardRemove assertions and a pre-existing pending request. Add both, plus real COMMIT failure and successful retry/reopen evidence. Existing trigger only fails INSERT.
3. Full admin dangerous callback dispatch with dry side-effect adapters remains missing (backup/reset/install/restart and other mutations).
4. Actual UsersShared update handler own/pending/close/remove/late response/two-admin behavior remains missing; reopen allocator test is not that handler acceptance or process crash test.
5. Wizard duplicate-email failure redraw, correction and successful retry behavior remains missing.

No second writer started. No production, merge, release, RN5, packaging, installer/script changes or global Hermes provider/config changes. Push only preserves this explicitly unaccepted checkpoint; CI green cannot clear the above gates.

## Authorized closeout follow-up (local acceptance, not production acceptance)

The preceding checkpoint limits are historical. This follow-up supplies real local Telegram + SQLite behavioral evidence, rather than treating gate approval as dispatch coverage:

- Issuance now uses `XUI_DB_FOLDER/telegram-binding-issuance.db`, separate from the ImportDB-replaceable business DB. A SQLite immediate transaction reads/increments the high-water mark, FULL synchronous COMMIT must succeed before publication, and the previous business ledger maximum is carried forward. Exhaustion returns before writing another reservation. Preserve this trusted file when moving an installation; do not restore it from an older business backup.
- Actual VACUUM backup, business-file restore and reopen: RED old=-1/new=-1; GREEN strictly newer negative issuance. Independent review additionally found old in-memory pending targets could alias different clients after restore. Actual isolated ImportDB with dry core stop/restart reproduced late binding to a different restored client (RED TgID=900); ImportDB now holds the Telegram interaction lock and clears pending before its first DB switch (GREEN no binding).
- Storage UPDATE failure and deferred-FK COMMIT failure begin with existing pending. Tests assert its revocation, no outbound request_users and an actual ReplyKeyboardRemove. The COMMIT injection separately proves UPDATE succeeds before COMMIT fails. Retry/reopen succeeds with the next committed ID; 24 concurrent same-process reservations are unique.
- Actual answerCallback dispatch exercises dry adapters for backup, subscription check/exec, installation and panel restart. Real isolated SQLite verifies traffic reset, all-traffic reset, quota, expiry, IP limit, IP clearing, nonzero Telegram binding removal and enable toggle. Ordinary users fail closed regardless of the supplied caller flag. Wizard submission is exercised separately with real database writes.
- Extracted handlers are the same functions registered by OnReceive, not a replacement implementation. UsersShared tests cover own successful binding, unknown/wrong request, nonadmin, wrong private recipient, duplicate response, close with keyboard removal, expiry, binding removal invalidating both admins, crossed admin requests, both admins' successful binding/removal and late responses. Current outbound request_users.request_id drives these handlers and is checked against pending.
- Real wizard callback submission receives duplicate-email failure, redraws fresh tokens while retaining ownership, rejects stale retry/other-admin input, accepts corrected email through the actual message handler, persists a successful retry and rejects a late submit without adding twice.
- Full regression exposed a no-core client-info nil panic; fixed with the normal offline path. Fake API cleanup waits for scheduled message deletes before closing the server/restoring global bot state.

Executed writer verification: `go test -vet=off ./...`, `go test -vet=off -race ./web/service`, all four existing Node suites, workflow-safety, Go build and rebuilt authenticated full-panel Chromium monthly help/tooltip with zero page errors and panel stopped. Detailed logs under `/root/.hermes/cache/scratch/stage1-closeout-*`; browser evidence `/root/.hermes/cache/scratch/stage1-monthly/evidence.json`. Existing two vet failures remain; no vet-clean claim.

Independent reviews completed read-only and exited before findings were edited. Reports: `stage1-closeout-independent-review.txt`, `stage1-closeout-final-review.txt`, and final P2 outbound-record isolation delta `stage1-closeout-review-delta.txt` under scratch. Final SHA and CI readback belong in the delivery report, not a self-referential commit field here.

Remaining evidence boundaries: no power-loss/process-crash test or cross-process reservation test; uniqueness depends on preserving the trusted external ledger, not deleting/rolling it back. Successful ImportDB late-response acceptance does not cover every inherited import failure/fallback path, active wizard/callback invalidation on import, or bot hot Stop/Start lifecycle. Dry adapters are not real host installation/restart acceptance. The shared owner-leased wizard is still serialized, not concurrent independent drafts. Do not describe these bounded tests as overall security/production acceptance. PR stays draft; no merge/release/package/installer/production or global Hermes changes authorized or performed.
