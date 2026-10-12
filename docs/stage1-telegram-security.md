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
Resume base exact SHA: e58910bffd5aaeb2b16c754c3a40ddf1e45be63b. This checkpoint is intentionally incomplete and PR #8 must remain draft. The code checkpoint SHA is recorded in the subsequent report commit (a commit cannot embed its own SHA).

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
