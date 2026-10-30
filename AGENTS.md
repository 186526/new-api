
## Fork Maintenance

This tree is a self-hosted fork of `QuantumNous/new-api` (remote `upstream`). It is maintained independently: upstream is a read-only reference, and only selected features are brought back.

### Upstream

- Do not merge `upstream/main` on a schedule. `master` is the deployed line and carries its own features.
- To bring an upstream feature in, cherry-pick the single commit and resolve conflicts against this tree. Do not merge an upstream branch wholesale.
- Keep the diff against upstream limited to the entries in the table below so later picks stay small.
- Verify a pick with `go build ./...` and the tests of the touched packages.

### Release

- Frontend: install the bun version named by `packageManager` in `web/package.json`, then run `bun install --frozen-lockfile` and `bun run build:check` in `web/`. `web/dist` is not tracked and must be rebuilt, because the Go build embeds it.
- Image: build the binary, then build a two-line Dockerfile on top of the previous image (`FROM <previous tag>`, `COPY new-api /new-api`), tag it `metrics-<YYYYMMDD>.<n>`, import it with `k3s ctr -n k8s.io images import`, then update the image tag in `deploys/30-new-api.yaml`. Do not use `podman commit` for this: podman copies the host's `http_proxy`/`https_proxy` into the image config, and the relay client reads `http.ProxyFromEnvironment`, so every upstream call then dials the host proxy from inside the pod and fails with `proxyconnect tcp: dial tcp 127.0.0.1:7897: connect: connection refused`. Check `podman inspect <tag> --format '{{json .Config.Env}}'` reports only `PATH` before importing.
- Tag the deployed commit `deploy-<YYYYMMDD>` and push the tag. Keep the previous image tag on the node; rolling back means re-applying the previous tag.

### Fork-owned changes

| Path | Purpose |
| --- | --- |
| `pkg/metrics/` | Prometheus endpoint, started only when `METRICS_ADDR` is set; `channel_balance.go` publishes the balance gauge for every channel whose balance query succeeds and the credit gauges for channels that report rolling windows |
| `controller/channel_billing_commandcode.go`, `controller/channel-billing.go` | `updateChannelBalance` publishes the balance gauge for every provider New API can query; the Command Code path additionally reads `/alpha/billing/credits` for every key of the channel, sums the accounts and publishes the rolling windows |
| `relay/channel/antigravity/`, `service/antigravity_oauth.go`, `service/antigravity_credential_refresh.go`, `service/antigravity_credential_refresh_task.go`, `service/antigravity_channel_models.go`, `service/antigravity_usage.go`, `controller/antigravity_usage.go` | native Antigravity channel: Google OAuth credential in the channel key, project bootstrap through `loadCodeAssist` / `onboardUser`, five-minute safe-window refresh, model discovery through `fetchAvailableModels`, account quota through `retrieveUserQuotaSummary` with a `retrieveUserQuota` fallback, and a relay adaptor that wraps the Gemini payload in the Antigravity envelope and unwraps the SSE frames. The upstream encodes the reasoning level in the model id, so the model list exposes base names only and `relay/channel/antigravity/modelname` appends the `-low` / `-medium` / `-high` suffix from the request effort, defaulting to medium; suffixed names still pass through, and model discovery folds the suffixes back |
| `constant/channel.go`, `constant/api_type.go`, `common/api_type.go`, `common/endpoint_type.go`, `relay/common/relay_info.go`, `relay/relay_adaptor.go`, `controller/channel.go`, `controller/channel_upstream_update.go`, `router/channel-router.go` | registration of channel type 64 and its four endpoints, the credential refresh route and the usage route |
| `relaykit/dto/channel_settings.go` | `BalanceSource` field and the `BalanceSourceCommandCode` constant |
| `main.go` | starts and shuts down the metrics server; starts the five-minute Command Code balance refresher and the ten-minute Antigravity credential refresher |
| `model/log.go` | settlement and task-billing metrics; resolves the user and channel names |
| `model/channel_cache.go` | publishes enabled models for the price collector |
| `model/log_other.go` | `publicInt` helper for consume-log fields |
| `pkg/perf_metrics/metrics.go` | `RecordRelayResult` takes the username label |
| `controller/relay.go` | relay and upstream-attempt metrics |
| `relay/responses_websocket.go` | username label for WebSocket relays |
| `Dockerfile` | exposes port 9090 |
| `web/src/routes/__root.tsx`, `web/src/routes/_authenticated/route.tsx`, `web/src/routes/(auth)/sign-in.tsx`, `web/src/features/auth/auth-layout.tsx` | sign-in gate for anonymous visitors |
| `web/src/features/channels/**` | balance source selector for OpenAI channels; Antigravity channel type, key validation, credential refresh and the usage dialog |
| `web/src/i18n/locales/*.json` | source-link keys, balance source labels and Antigravity copy |
| `README.md` | describes the site customization |
| `.github/workflows/site-image.yml`, `scripts/check-site-frontend.mjs` | frontend checks for the sign-in gate; the workflow still targets the removed `site/login-required` branch |

The parent repository keeps the deployment and dashboards: `kubernetes/new-api/deploys/` holds the manifests, and `kubernetes/new-api/monitoring/` holds the Grafana dashboard, which is organised as 总览, 渠道与额度, 收入与用量, 质量与性能 and a collapsed 基础设施 row, plus the Prometheus alert rules.
