# Traffic identity retirement (design)

Client removal must commit a persistent retirement record in the same SQLite
transaction as settings/statistics removal, under the collector mutex. The record
owns the complete retired statistics snapshot and accepts late destructive core
deltas, including retained failed batches. Known retired identities are not
unknown identities. Active and retired deltas commit together; on rollback the
collector retains the original batch. Core changes remain strictly post-commit;
core failure requests reconciliation/restart, never replay of committed deltas.

Rename uses a persistent old-email -> current-email alias, migrated transitively
with subsequent renames. Late counters resolve to the current statistics row;
monthly configuration and accumulated usage migrate unchanged. After removal,
all aliases continue to resolve to the retired canonical owner. Inbound removal
similarly archives its tag and
aggregate counters before deleting the live row. Depleted cleanup uses the same
retirement functions. Repeating removal creates no second archive or delta.

Retired emails/tags are reserved (case-insensitive emails, as upstream) rather
than reused while old core counters can still arrive. No automatic time-based
purge: neither RemoveUser success nor an empty poll proves existing connections
cannot produce late traffic. This local restriction is not a global edit ban;
ordinary edits, other clients, and collector retries continue. Truly unknown
nonzero identities remain explicit integrity errors, not silently discarded.

Online inbound creation/import validates identities from Settings regardless of
whether ClientStats is supplied. Nonempty imported statistics must match clients
one-to-one by exact email (not merely case-insensitively); missing, duplicate or
unrelated rows are rejected without persistence or core calls. Valid imports keep
usage counters, allocate fresh statistics IDs, bind the new inbound, and enroll
monthly settings from the actual clients. Empty statistics retain ordinary
creation behavior. This is distinct from offline whole-database restoration.

Client retirement uses an explicit snapshot schema including
`monthly_reset_day` and `monthly_reset_at`, without exposing those internal fields
in public ClientTraffic JSON. Late deltas update archive counters, not the
historical snapshot. Existing incomplete snapshots cannot recover omitted fields
and are not backfilled with guessed values.

Scope: small additive model/migration and service helpers, no unrelated modules.
Persistent ownership survives panel restart; the existing in-memory destructive
read-to-commit crash window is **not** made durable by this design. Archives can
grow with removed identities; safe garbage collection/reuse needs a separately
verified core-generation boundary and is not part of this change.

Acceptance: old=7/new=3 after removal, continuing healthy deltas/monthly reset;
SQLite write/commit failure and retry; post-commit RemoveUser failure; client,
inbound, bulk and depleted removal; rename chains and pending retry; real loopback
gRPC injection; full Go/race/build and frontend tests, draft PR CI only.
