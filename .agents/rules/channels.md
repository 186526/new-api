# Channel Rules

Mandatory conventions for channel balance metrics, channel type 64, the Command Code, Antigravity and Mistral paths, the usage dialogs and the channel settings transport fields. `AGENTS.md`'s **Rule files** table names this file for that scope; a task that matches MUST read this whole file before planning, coding, or reviewing. Rules use the same MUST / Do NOT wording as `AGENTS.md` and carry the same weight.

## Metrics and balance publishing

- `pkg/metrics/` serves the Prometheus endpoint, started only when `METRICS_ADDR` is set.
- `pkg/metrics/channel_balance.go` publishes the balance gauge for every channel whose balance query succeeds, and the credit gauges for channels that report rolling windows.
- `pkg/metrics/upstream_quota.go` publishes the remaining fraction for upstreams that report quota windows instead of a currency amount.
- `updateChannelBalance` (`controller/channel-billing.go`) publishes the balance gauge for every provider New API can query.
- The Command Code path (`controller/channel_billing_commandcode.go`) additionally reads `/alpha/billing/credits` for every key of the channel, sums the accounts and publishes the rolling windows.
- The Antigravity path (`controller/channel_billing_antigravity.go`) polls `retrieveUserQuotaSummary` for every type 64 channel and publishes `newapi_upstream_quota_remaining_ratio` per scope and window.

## Channel type 64 registration

Channel type 64 and its four endpoints, the credential refresh route and the usage route are registered across `constant/channel.go`, `constant/api_type.go`, `common/api_type.go`, `common/endpoint_type.go`, `relay/common/relay_info.go`, `relay/relay_adaptor.go`, `controller/channel.go`, `controller/channel_upstream_update.go` and `router/channel-router.go`. A new endpoint of the channel type MUST be added in all of them.

## Antigravity channel

- The native Antigravity channel lives in `relay/channel/antigravity/` with `service/antigravity_oauth.go`, `service/antigravity_credential_refresh.go`, `service/antigravity_credential_refresh_task.go`, `service/antigravity_channel_models.go`, `service/antigravity_usage.go` and `controller/antigravity_usage.go`.
- The Google OAuth credential is stored in the channel key. The project is bootstrapped through `loadCodeAssist` / `onboardUser`, refreshed inside a five-minute safe window, its models discovered through `fetchAvailableModels`, and its account quota read through `retrieveUserQuotaSummary` with a `retrieveUserQuota` fallback.
- The relay adaptor wraps the Gemini payload in the Antigravity envelope and unwraps the SSE frames.
- The upstream encodes the reasoning level in the model id, so `relay/channel/antigravity/modelname` holds one entry per `fetchAvailableModels` model with the suffixes that model actually serves. The model list exposes base names only, and the adaptor appends the level of the request effort:
  - `xhigh` and `max` both mean the top level.
  - `gemini-4-neon`, `gemini-4-hydrogen` and `gemini-4-hydrogen-tiered` declare `xhigh` and get it; every other model falls back to `high`.
  - An effort the model does not serve falls back to its default level, which is `high` for `gemini-4-hydrogen-tiered`.
  - Unsuffixed names and names outside the table are sent unchanged.
- Model discovery folds a declared suffix back to the base name and drops models the payload marks `disabled`, such as `gemini-4-carbon-disabled`, which only Ultra subscribers can use.
- Refresh that table from a `fetchAvailableModels` response when the upstream adds or retires models.

## Mistral merged monthly allowance

`service/mistral_quota.go`, `controller/mistral_quota.go` and `controller/channel_billing_mistral.go` serve the merged monthly allowance.

- Account entries come from `MISTRAL_VIBE_QUOTA_ACCOUNTS`, `MISTRAL_VIBE_QUOTA_ACCOUNTS_FILE`, or the single-account `MISTRAL_VIBE_QUOTA_CHANNEL` with `MISTRAL_VIBE_QUOTA_COOKIE`, `MISTRAL_VIBE_QUOTA_VIBE_KEY` or `MISTRAL_VIBE_QUOTA_STUDIO_KEY`.
- An account entry requires a `channel` plus at least one of `cookie`, `vibeKey` or `studioKey`; a key alone is a valid entry, and an entry with none of the three is rejected.
- Vibe and Studio share one "Included Vibe & Studio usage" allowance, so the account reports one figure rather than a budget per plan side.
- The allowance is read from the first source that answers:
  1. `api.mistral.ai/v1/billing/budget` with the account's key, which carries `usage_percentage` and `initial_budget` per plan side and names the plan the key is billed on.
  2. Whenever the entry also carries a `cookie`, the console `admin.mistral.ai/api/trpc/billing.budget` with that cookie, which carries the merged percentage and the plan amount.
  3. `chat.mistral.ai/api/code-trpc/apiKey.getUsage` with the same cookie, which carries the percentage alone.
- A cookie with no key is read on its own, and a key the billing endpoint refuses falls through to the cookie. A 401 or 403 on the key, or a 302 on the cookie, falls through to the next source.
- `MistralQuotaAuthError` names the rejected credential and is reported only when nothing answers, which backs the loop off for up to an hour.
- The key endpoint's own `initial_budget` is kept, because it names the plan that key is billed on, and `monthlyLimitUsd` fills in only an answer that reports none.
- A key of a free plan reports that plan's 10 USD, which is why the allowance is read with the key the channel itself carries — `ChannelKey`, filled by `mistralChannelKey` in `controller/channel_billing_mistral.go`, which takes the first key of `channel.GetKeys()` — rather than with a key written into the accounts file.
- Mistral no longer renders the allowance to a live console session: `admin.mistral.ai` answers a dead cookie with 200 plus the login page, which is why the key comes first. Cookie values are sent without the quotes they are copied with, because the quoted form answers the login redirect on a session that is still valid.
- Each tick publishes `newapi_upstream_quota_remaining_ratio` with scope `vibe` and window `monthly`, the label the dashboard panels and the quota alert group by. The refresh reads each account once per tick and publishes that single window.
- The channel balance is `initial_budget * (1 - usage_percentage/100)` for every Mistral channel of that account, read with that channel's own key. The same refresh stores the balance and publishes `newapi_channel_balance_usd`.
- The balance sweep, the quota refresh and the `/api/channel/:id/mistral/usage` dialog all resolve the channel first, so a stale key in the accounts file cannot decide a live channel's balance.
- The channel row is loaded with its key (`GetChannelById(id, true)`), because `false` omits the `key` column.
- The refresh does not pause between accounts, so the refresh interval alone sets how often the upstream is asked.

## Mistral Vibe keys and request conversion

- Vibe plan keys (`mstrl_`) send the Vibe CLI User-Agent on relayed model calls and on the `/v1/models` fetch.
- The Mistral channel keeps no provider-specific model list and follows the upstream model list through the generic fetch and auto-sync.
- The request conversion maps the OpenAI `developer` role to `system`, the only role set Mistral accepts. `/v1/responses` requests are forwarded to Mistral's own Responses endpoint with the same role rewrite.
- The chat conversion forwards the stored client body itself and rewrites only the members Mistral rejects, because the upstream prompt cache is keyed on the request body and rebuilding it misses the cache on every call, which bills the whole prompt at ten times the input rate:
  - A `tool_calls[].id` or `tool_call_id` that is not nine alphanumerics becomes the first nine hex digits of its SHA-256, so the same id reads back the same in every turn of a conversation.
  - `role: developer` becomes `system`.
  - A tool message keeps its content as the string of its text parts.
  - An assistant message that carries tool calls and empty content gets `content: null`.
  - `reasoning_content` is dropped, because Mistral rejects every member a message does not declare.
  - Every other member keeps its value and its position in the body.
- Two values come from the typed request instead of the body: `model`, because the relay layer resolves the channel `model_mapping` before the adaptor runs and the body still names the model the client asked for, and `metadata`, when the body carries none, because the adaptor tags it for the Vibe allowance.
- A body read without a stored request falls back to the parsed request, and so does a stored body that holds nothing: the empty storage is never parsed, because `encoding/json` answers it with `unexpected end of JSON input`. The channel test in `controller/channel-test.go` produces exactly that storage, since it builds its request in memory and sends no body.
- The answer comes back as content parts when the request names a reasoning effort: `content` holds a list whose `thinking` parts carry the reasoning and whose `text` parts carry the answer, in both the streaming deltas and the non-streaming message. `relay/channel/mistral/content_parts.go` rewrites both into the OpenAI shape before the OpenAI handlers read them, so `content` is the string of its text parts and the reasoning lands in `reasoning_content`. A frame whose content is already a string is passed through byte for byte, and the non-streaming body is read and replaced whole. Without the rewrite the OpenAI stream parser fails every thinking frame with `cannot unmarshal array into … delta.content of type string`, drops the reasoning, and the client never sees it.
- The channel setting `mistral_vibe_attribution` turns off the Vibe tag for a plan key and leaves its requests on the API allowance.

## Codex HTTP conversion

- `relay/compatible_handler.go` routes Chat Completions to Responses when the selected channel is Codex and request-body passthrough is off. This does not require the global conversion policy.
- `relay/channel/codex/adaptor.go` sends regular Responses requests upstream with `stream:true`, even for clients that requested JSON. A successful response without `Content-Type` is treated as an event stream. Non-streaming clients receive JSON built from that stream. Responses Compact and Alpha Search retain their own request and response formats.

## Usage dialogs

`GET /api/channel/:id/mistral/usage` and `GET /api/channel/:id/commandcode/usage` (`controller/channel_usage_mistral.go`, `controller/channel_usage_commandcode.go`) back the two dialogs.

- The first returns the account's merged monthly allowance as a one-element `budgets` array with scope `vibe` without touching the database, read with the channel's own key (the row is loaded with `GetChannelById(id, true)`, because `false` omits the `key` column).
- The second lists every key of a Command Code channel with its remaining credits and rolling windows, and never returns the key.

## Channel settings transport fields

`relaykit/dto/channel_settings.go`:

- `BalanceSource` and the `BalanceSourceCommandCode` constant.
- `ResponseHeaderTimeoutSeconds` and `ResponseHeaderTimeoutRetrySeconds` override the global upstream header wait per channel, and `service/http_transport_policy.go` folds them into the cached client key so channels with different timeouts never share a transport.
- `MaxImagesPerRequest` is the channel-wide upstream image ceiling and `MaxImagesPerModel` holds one ceiling per upstream model. `ValidateHTTPTransport` rejects both as negative, and additionally requires a non-empty model name on a per-model entry.
