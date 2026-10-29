
## Fork Maintenance

This tree is a self-hosted fork of `QuantumNous/new-api` (remote `upstream`). It stays a thin layer over upstream so every sync remains a plain merge.

### Sync

- Merge `upstream/main` once a week with a merge commit. Do not rebase the pushed branch and do not cherry-pick: a picked subset turns every later upstream change to the same file into a manual conflict.
- Before merging, list the upstream `feat:` commits for the owner to approve. Bug fixes, tests, chores and dependency bumps merge without a separate review.
- Resolve conflicts in favor of upstream structure, defaults and behavior, then reattach our call sites. For the i18n locale files keep the keys added by both sides.
- After a merge, recompute the last 24 hours of usage logs under the new billing expressions. Ship when the total rises less than 10%; above that, report the number and stop.
- Verify with `go build ./...` and `go test ./pkg/... ./service/... ./relay/helper/...`. Fix until green; changes on both sides are allowed, and changing ours is preferred.

### Release

- Frontend: install the bun version named by `packageManager` in `web/package.json`, then run `bun install --frozen-lockfile` and `bun run build:check` in `web/`. `web/dist` is not tracked and must be rebuilt, because the Go build embeds it.
- Image: build the binary, copy it over the previous base image, tag it `metrics-<YYYYMMDD>.<n>`, import it with `k3s ctr -n k8s.io images import`, then update the image tag in `deploys/30-new-api.yaml`.
- Tag the deployed commit `deploy-<YYYYMMDD>` and push the tag. Keep the previous image tag on the node; rolling back means re-applying the previous tag.

### Fork-owned changes

| Path | Purpose |
| --- | --- |
| `pkg/metrics/` | Prometheus endpoint, started only when `METRICS_ADDR` is set |
| `main.go` | starts and shuts down the metrics server |
| `model/log.go` | settlement and task-billing metrics; resolves the user and channel names |
| `model/channel_cache.go` | publishes enabled models for the price collector |
| `model/log_other.go` | `publicInt` helper for consume-log fields |
| `pkg/perf_metrics/metrics.go` | `RecordRelayResult` takes the username label |
| `controller/relay.go` | relay and upstream-attempt metrics |
| `relay/responses_websocket.go` | username label for WebSocket relays |
| `Dockerfile` | exposes port 9090 |
| `web/src/routes/__root.tsx`, `web/src/routes/_authenticated/route.tsx`, `web/src/routes/(auth)/sign-in.tsx`, `web/src/features/auth/auth-layout.tsx` | sign-in gate for anonymous visitors |
| `web/src/i18n/locales/*.json` | source-link keys |
| `README.md` | describes the site customization |
| `.github/workflows/site-image.yml`, `scripts/check-site-frontend.mjs` | frontend checks for the sign-in gate; the workflow still targets the removed `site/login-required` branch |
