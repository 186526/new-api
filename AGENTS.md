# AGENTS.md — this fork's conventions

## Fork Maintenance

This tree is a self-hosted fork of `QuantumNous/new-api` (remote `upstream`). It is maintained independently: upstream is a read-only reference, and only selected features are brought back.

### Upstream

- Do not merge `upstream/main` on a schedule. `master` is the deployed line and carries its own features.
- To bring an upstream feature in, cherry-pick the single commit and resolve conflicts against this tree. Do not merge an upstream branch wholesale.
- Keep the diff against upstream limited to the rule files and the index below, so later picks stay small.
- Verify a pick with `go build ./...` and the tests of the touched packages.
- The deployed commit is tagged `deploy-<YYYYMMDD>`; `git log deploy-<YYYYMMDD>..master` lists what is built but not yet released.

### Remotes

- The checkout on this workstation is often on a detached HEAD while the local `master` ref lies behind `origin/master`. Check `git status -sb` for `HEAD（非分支）` before pushing; when it is detached, push the commit you are on instead: `git push origin HEAD:master`.
- `origin` is `git@github.com:186526/new-api.git`. The deployment repository `https://git.186526.xyz/186526/kubernetes.git` is the parent tree's remote and holds `deploys/` and `monitoring/`. Push the fork to `git.186526.xyz` over HTTPS when GitHub is unreachable.
- The parent repository tracks `source/` as a gitlink (mode 160000). Bump it to the new `master` commit whenever a parent change depends on fork code; the recorded commit must be reachable from the fork remote, because a fresh clone fetches only what the remote has. Bumping the gitlink is a separate step from the code commit, and the image tags in `deploys/` name the `sha-<commit>` CI built.

### Release

- Every image is built by CI. Do not build images locally, and do not hand-assemble one on the workstation: `.github/workflows/master-image.yml` runs on every push to `master` and publishes `ghcr.io/186526/new-api:sha-<commit>` from the `backend` target (`-tags no_web_assets`) and `sha-<commit>-web` from the `web` target.
- Before pushing, run the checks CI runs: `go build ./...` and the tests of the touched packages here, then `bun install --frozen-lockfile`, `bun check -b` and `bun run lint` in `web/`. A red check belongs in the commit that fixes it.
- The CI test step is `make test`: `GOWORK=off go test ./...` on the root module plus one run per `relaykit` and `tokenkit` (`ci.yml`). `./...` includes package main, so `main_test.go` runs; it reaches neither sub-module because each has its own `go.mod`. The root run needs `web/dist` because package main embeds it, so CI creates `web/dist/index.html` first — a local run without a dashboard build can pass `-tags no_web_assets` instead.
- The race detector is not a gate. Run `go test -race -count=1 ./<package>/` yourself after touching a package's test helpers, and keep what passes `-race` passing. `relay/channel/gemini` is such a package: `TestMain` sets `gin.SetMode(gin.TestMode)` and `constant.StreamingTimeout` once, and no test writes either global.
- The node pulls from GHCR directly, so an image never has to be copied onto it: `k3s ctr -n k8s.io images pull --platform linux/amd64 ghcr.io/186526/new-api:sha-<commit>`, and the same with `-web`. A backend-only change needs no new web image.
- Do not hand-assemble an image archive and `k3s ctr -n k8s.io images import` it: such archives fail there, typically with `ctr: unexpected EOF`, and a manifest naming a blob the content store lacks fails earlier with `content digest ... not found`. A `pull` fails neither way.
- Keep the previous image tag on the node; rolling back means re-applying it. `imagePullPolicy: IfNotPresent` still pulls a tag the node does not have, so pull the previous tag first if `crictl images` no longer lists it, or the Deployment stalls on a failed pull.
- After a release, `crictl rmi` every tag that is neither current nor kept for rollback, plus every `<none>` entry. Removing by ID also removes other tags sharing it, so remove unwanted tags by name when a tag to keep shares the ID.

## Rule files

`.agents/rules/` holds the detail; this file holds the index. Read the matching file in full before planning, coding, or reviewing a change in its area — a grep or a skim does not count. Rules in those files carry the same weight as the ones here.

| File | Read it when the change touches |
| --- | --- |
| `billing.md` | quota, pricing, pre-consume, settlement, refund, or any quantity derived from an upstream response |
| `channels.md` | a channel adaptor, channel balance, Command Code or Antigravity or Mistral quota, a usage dialog, or `relaykit/dto/channel_settings.go` |
| `image-limit.md` | `relay/common/image_limit.go`, `relay/image_limit.go`, or any code that counts images in a request |
| `websearch.md` | `service/websearch/`, the five search and fetch routes, or `tools-search` / `tools-fetch` |
| `systemone.md` | the System One contract, `relay/systemone_*`, or `relaykit/systemone/` |
| `region-lease.md` | `REGION_LEASE_ENABLED`, `model/region_pool.go`, `model/quota_reserve.go`, or the user and token quota ledgers |
| `process-lifecycle.md` | `SERVER_ROLE`, `REGION`, `main.go`'s startup or shutdown path, or `middleware/draining.go` |
| `relay-transport.md` | stream keepalive, upstream header wait, request-log redaction, relay metrics, or the embedded-versus-nginx asset split |

## Index of fork-owned code

Rows here are short facts; the rule files above hold the reasoning.

| Path | Purpose |
| --- | --- |
| `main.go` | starts and shuts down the metrics server; starts the five-minute Command Code balance refresher, the five-minute Antigravity quota refresher, the ten-minute Antigravity credential refresher and, when a Mistral account source is configured, the Mistral quota refresher, plus the `CHANNEL_UPDATE_FREQUENCY` channel-balance refresh that runs once at startup |
| `model/log.go` | settlement and task-billing metrics; resolves the user and channel names |
| `model/channel_cache.go` | publishes enabled models for the price collector |
| `model/log_other.go` | `publicInt` helper for consume-log fields |
| `pkg/perf_metrics/metrics.go` | `RecordRelayResult` takes the username label |
| `Dockerfile` | exposes port 9090 |
| `web/src/routes/__root.tsx`, `web/src/routes/_authenticated/route.tsx`, `web/src/routes/(auth)/sign-in.tsx`, `web/src/features/auth/auth-layout.tsx` | sign-in gate for anonymous visitors |
| `web/src/features/channels/**` | balance source selector for OpenAI channels; Antigravity channel type, key validation, credential refresh and the usage dialog; usage dialogs for Mistral channels and for Command Code balance sources |
| `web/src/features/api-docs/`, `web/src/routes/docs/` | the `/docs` API reference page: one section per protocol the relay serves, each with its `base_url`, authentication, endpoint table and a runnable curl and Python example. The base address and model list come from `/api/status` and `/api/pricing` rather than being written into the page |
| `web/src/i18n/locales/*.json` | source-link keys, balance source labels and Antigravity copy |
| `README.md` | describes the site customization |
| `.github/workflows/site-image.yml`, `scripts/check-site-frontend.mjs` | frontend checks for the sign-in gate; the workflow still targets the removed `site/login-required` branch |
| `.github/workflows/master-image.yml` | builds the `linux/amd64` image from every push to `master` and publishes it as `ghcr.io/186526/new-api:sha-<commit>` |

The parent repository keeps the deployment and dashboards: `kubernetes/new-api/deploys/` holds the manifests, and `kubernetes/new-api/monitoring/` holds the Grafana dashboard, which is organised as 总览, 渠道与额度, 收入与用量, 质量与性能 and a collapsed 基础设施 row, plus the Prometheus alert rules.
