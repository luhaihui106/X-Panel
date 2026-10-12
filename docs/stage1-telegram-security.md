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
