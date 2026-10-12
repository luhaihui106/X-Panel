# Homepage promotion removal (downstream patch)

## Scope and upstream maintenance

Baseline: downstream `19ad64dfccdf877dfe8695ee9cddfa11df310797`, based on
upstream v26.9.13 (`8f6bcd84abe74c9374ae929d28df092e0acd75f3`).

Only `web/html/index.html` behavior changes: remove the automatic free-edition
Pro purchase welcome modal, its `welcomeModal` state, `welcomeShown` session
storage reads/writes, `closeWelcomeModal`, and `jumpToProFeatures` redirect.
Do not replace it with a hidden modal or set a storage flag to suppress it.

Homepage audit found **one** automatic Pro promotion: the `mounted` welcome
modal. The purchase link formerly near line 1260 belongs to the **user-clicked**
remote backup/restore feature restriction, not another automatic promotion.
That restriction and purchase link remain unchanged. Backup/restore are not
implemented or unlocked by this patch. Other navigation/inbounds/servers
promotions, status polling, security alerts, functional modals, sidebar copyright,
GPL license, branding and authorization behavior are outside this removal.

When updating upstream, inspect `index.html` modal markup, Vue `data`, `methods`,
and lifecycle hooks together. Reapply only this bounded removal; run the source
regression and authenticated full-page browser acceptance against the new build.
A source marker check is not a substitute for rendered-page acceptance.

## Regression and acceptance

```sh
node --test web/assets/js/util/home-promotion.test.cjs \
  web/assets/js/model/monthly-reset.test.cjs \
  web/assets/js/util/traffic-identity.test.cjs
go test -vet=off ./...
go test -vet=off -race ./web/service ./web/controller
go build -o "$TMPDIR/x-panel-homepage-check" .
```

Unmodified baseline `go test ./...` fails on two inherited vet findings:
`web/service/tgbot.go:3540` (function comparison) and
`web/service/server.go:1375` (nonconstant format string). The bounded patch does
not change those files; `-vet=off` is explicit, not a claim of a clean vet run.

TDD evidence for this patch:
- Before removal, the new Node test failed on `welcome-modal`; its preservation
  assertion passed. The real baseline binary also failed browser acceptance:
  free-edition Pro welcome text rendered and one automatic modal was visible.
- After removal, all four Node subtests passed; Go tests, service/controller race
  tests and native Linux ARM64 build passed.
- A fresh authenticated Chromium context used the complete served homepage and
  a fresh loopback-only SQLite panel, no mocked HTTP/Vue/components, no core
  assets and notifications/subscriptions disabled. No automatic modal, no
  dedicated promotion state/session key, at least three successful live status
  polls, no page errors or external page requests. Actual sidebar menu click
  opened Inbounds; returning home stayed advertisement-free. User-requested
  remote backup still displayed its feature restriction; the real local backup
  modal remained available. The isolated panel process was stopped afterward.

Local detailed evidence: `/root/.hermes/cache/scratch/home-promotion/` contains
`browser.py`, `browser-red.log`, `browser-green.log`, `red/evidence.json`,
`green/evidence.json`, homepage/backup screenshots and native test binaries.
Node red evidence is `/root/.hermes/cache/scratch/home-promotion-red.log`;
Go baseline evidence is `/root/.hermes/cache/scratch/home-go-baseline.log`.
These are local acceptance artifacts, not release packages or production state.

Only the feature branch / existing draft PR is updated. No merge, release,
multi-architecture distribution matrix or production deployment is authorized.
