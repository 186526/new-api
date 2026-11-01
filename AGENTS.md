
## Fork Maintenance

This tree is a self-hosted fork of `QuantumNous/new-api` (remote `upstream`). It is maintained independently: upstream is a read-only reference, and only selected features are brought back.

### Upstream

- Do not merge `upstream/main` on a schedule. `master` is the deployed line and carries its own features.
- To bring an upstream feature in, cherry-pick the single commit and resolve conflicts against this tree. Do not merge an upstream branch wholesale.
- Keep the diff against upstream limited to the entries in the table below so later picks stay small.
- Verify a pick with `go build ./...` and the tests of the touched packages.

### Release

- Frontend: install the bun version named by `packageManager` in `web/package.json`, then run `bun install --frozen-lockfile` and `bun run build:check` in `web/`. `web/dist` is not tracked and must be rebuilt, because the Go build embeds it.
- Image: every push to `master` runs `.github/workflows/master-image.yml`, which publishes `ghcr.io/186526/new-api:sha-<commit>` for `linux/amd64`. The deployment pins that tag, so the running image names its commit.
- When the node cannot pull from GHCR, build the binary, then build a two-line Dockerfile on top of the previous image (`FROM <previous tag>`, `COPY new-api /new-api`), tag it `sha-<commit>`, import it with `k3s ctr -n k8s.io images import`, then set the same tag in `deploys/30-new-api.yaml`. Do not use `podman commit` for this: podman copies the host's `http_proxy`/`https_proxy` into the image config, and the relay client reads `http.ProxyFromEnvironment`, so every upstream call then dials the host proxy from inside the pod and fails with `proxyconnect tcp: dial tcp 127.0.0.1:7897: connect: connection refused`. Check `podman inspect <tag> --format '{{json .Config.Env}}'` reports only `PATH` before importing.
- Tag the deployed commit `deploy-<YYYYMMDD>` and push the tag. Keep the previous image tag on the node; rolling back means re-applying the previous tag.
- When the link to the node is slow, do not stream the whole archive: every image differs from its base in one layer, so export the OCI image with `podman save --format oci-archive`, keep `index.json`, `oci-layout`, the manifest, the config and the last layer blob, then import that 40-45 MB archive with `k3s ctr -n k8s.io images import --no-unpack -`. The importer resolves the base layers from the content store, and the 1.4 GB full transfer that used to take 20 minutes finishes in under a minute.

### Fork-owned changes

| Path | Purpose |
| --- | --- |
| `pkg/metrics/` | Prometheus endpoint, started only when `METRICS_ADDR` is set; `channel_balance.go` publishes the balance gauge for every channel whose balance query succeeds and the credit gauges for channels that report rolling windows; `upstream_quota.go` publishes the remaining fraction of upstreams that report quota windows instead of a currency amount |
| `controller/channel_billing_commandcode.go`, `controller/channel-billing.go`, `controller/channel_billing_antigravity.go` | `updateChannelBalance` publishes the balance gauge for every provider New API can query; the Command Code path additionally reads `/alpha/billing/credits` for every key of the channel, sums the accounts and publishes the rolling windows; the Antigravity path polls `retrieveUserQuotaSummary` for every type 64 channel and publishes `newapi_upstream_quota_remaining_ratio` per scope and window |
| `relay/channel/antigravity/`, `service/antigravity_oauth.go`, `service/antigravity_credential_refresh.go`, `service/antigravity_credential_refresh_task.go`, `service/antigravity_channel_models.go`, `service/antigravity_usage.go`, `controller/antigravity_usage.go` | native Antigravity channel: Google OAuth credential in the channel key, project bootstrap through `loadCodeAssist` / `onboardUser`, five-minute safe-window refresh, model discovery through `fetchAvailableModels`, account quota through `retrieveUserQuotaSummary` with a `retrieveUserQuota` fallback, and a relay adaptor that wraps the Gemini payload in the Antigravity envelope and unwraps the SSE frames. The upstream encodes the reasoning level in the model id, so the model list exposes base names only and `relay/channel/antigravity/modelname` appends the `-low` / `-medium` / `-high` suffix from the request effort, defaulting to medium; suffixed names still pass through, and model discovery folds the suffixes back |
| `constant/channel.go`, `constant/api_type.go`, `common/api_type.go`, `common/endpoint_type.go`, `relay/common/relay_info.go`, `relay/relay_adaptor.go`, `controller/channel.go`, `controller/channel_upstream_update.go`, `router/channel-router.go` | registration of channel type 64 and its four endpoints, the credential refresh route and the usage route |
| `service/mistral_vibe_quota.go`, `controller/mistral_vibe_quota.go`, `controller/channel_billing_mistral.go` | Mistral Vibe monthly allowance: reads session cookies from `MISTRAL_VIBE_QUOTA_ACCOUNTS`, `MISTRAL_VIBE_QUOTA_ACCOUNTS_FILE` or `MISTRAL_VIBE_QUOTA_COOKIE`, queries `chat.mistral.ai/api/code-trpc/apiKey.getUsage` with browser request headers, backs off for an hour after a rejected session, and publishes `newapi_upstream_quota_remaining_ratio` with scope `vibe` and window `monthly`. The channel balance path matches a Mistral channel to the account of the same name and reports `monthlyLimitUsd` minus the used percentage, so the channel row and the balance refresher show the remaining allowance |
| `relay/channel/mistral/`, `controller/channel.go` | Vibe plan keys (`mstrl_`) send the Vibe CLI User-Agent on relayed model calls and on the `/v1/models` fetch; the Mistral channel keeps no provider-specific model list and follows the upstream model list through the generic fetch and auto-sync. The request conversion maps the OpenAI `developer` role to `system`, which is the only role set Mistral accepts, and `/v1/responses` requests are converted to Chat Completions for the upstream and converted back in the response |
| `controller/channel_usage_mistral.go`, `controller/channel_usage_commandcode.go` | `GET /api/channel/:id/mistral/usage` and `GET /api/channel/:id/commandcode/usage` back the two usage dialogs: the first reads the Vibe allowance of the account named after the channel without touching the database, the second lists every key of a Command Code channel with its remaining credits and rolling windows and never returns the key |
| `relaykit/dto/channel_settings.go` | `BalanceSource` field and the `BalanceSourceCommandCode` constant |
| `relay/systemone_handler.go`, `relaykit/dto/systemone_request.go`, `relay/helper/valid_request.go`, `relay/common/relay_info.go`, `relay/constant/relay_mode.go`, `router/relay-router.go`, `controller/relay.go` | `/v1/systemone` passthrough for TypeSafe decision models: the body is forwarded unchanged apart from a mapped `model` field, the response keeps its answers but reports the model name the client asked for and drops the provider's `opencode` account block, and settlement uses the upstream `usage.input_tokens` / `output_tokens` |
| `main.go` | starts and shuts down the metrics server; starts the five-minute Command Code balance refresher, the five-minute Antigravity quota refresher, the ten-minute Antigravity credential refresher and, when a Vibe account source is configured, the Mistral Vibe quota refresher, plus, when `CHANNEL_UPDATE_FREQUENCY` is set, the minute-interval refresh of every channel balance that runs once at startup |
| `model/log.go` | settlement and task-billing metrics; resolves the user and channel names |
| `model/channel_cache.go` | publishes enabled models for the price collector |
| `model/log_other.go` | `publicInt` helper for consume-log fields |
| `pkg/perf_metrics/metrics.go` | `RecordRelayResult` takes the username label |
| `controller/relay.go` | relay and upstream-attempt metrics |
| `relay/responses_websocket.go` | username label for WebSocket relays |
| `Dockerfile` | exposes port 9090 |
| `web/src/routes/__root.tsx`, `web/src/routes/_authenticated/route.tsx`, `web/src/routes/(auth)/sign-in.tsx`, `web/src/features/auth/auth-layout.tsx` | sign-in gate for anonymous visitors |
| `web/src/features/channels/**` | balance source selector for OpenAI channels; Antigravity channel type, key validation, credential refresh and the usage dialog; usage dialogs for Mistral channels and for Command Code balance sources |
| `web/src/i18n/locales/*.json` | source-link keys, balance source labels and Antigravity copy |
| `README.md` | describes the site customization |
| `.github/workflows/site-image.yml`, `scripts/check-site-frontend.mjs` | frontend checks for the sign-in gate; the workflow still targets the removed `site/login-required` branch |
| `.github/workflows/master-image.yml` | builds the `linux/amd64` image from every push to `master` and publishes it as `ghcr.io/186526/new-api:sha-<commit>` |

The parent repository keeps the deployment and dashboards: `kubernetes/new-api/deploys/` holds the manifests, and `kubernetes/new-api/monitoring/` holds the Grafana dashboard, which is organised as 总览, 渠道与额度, 收入与用量, 质量与性能 and a collapsed 基础设施 row, plus the Prometheus alert rules.
