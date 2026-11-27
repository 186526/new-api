# Web Search and Fetch Rules

The `service/websearch` pipeline, the five routes into it, the `tools-search` / `tools-fetch` pricing, and the rendered Kimi Code contract.

## Pipeline layers

`service/websearch/` is the pipeline every caller shares, in three layers that do not know each other.

- `abstract/`: contracts (`tools/search`, `tools/fetch`, `alpha/search`, `moonshot/search`, `moonshot/fetch`); one parser per provider family (`deepseek`, `codex`, `antigravity`, `exa`, `local`); `Candidates`, returning every provider this deployment can reach for a contract, best first.
- `request/`: one requester per provider, plus `transport.go`'s `Forward` — the only place retry, cache and size policy lives. 408/429/5xx retry up to `retry_attempts`; a spent budget returns the last upstream answer, not an error, so the client reads the upstream status through `status_code_mapping`. Forward cache keyed `sha256(method + URL + cacheKey)`, storing only a 2xx with a body.
- `response/`: renders abstract objects into the client's contract.
- Any provider serves any contract; `Native` affects rank only, not admission.
- A candidate is skipped only when its request provably never left the process (`request.WasSent` false) — retrying a request that may have been billed pays twice.
- A native candidate's body is forwarded byte for byte, keeping DeepSeek's `/v1/tools/*` and Codex's `/v1/alpha/search` answers exactly what they were.

`Candidates` orders by origin: operator binding, the channel's own native upstream, the channel's own rendered upstream, a provider the global settings hold a key for, then `local`. `websearch_setting.candidate_order` reorders without adding or removing a candidate; availability comes from a credential, never the order. `local` is always last on a fetch, even when named first.

## Antigravity search

Citation short links are resolved before rendering, cached in `websearch:redirect:v1` (24 h by default, negative TTL disables).

- Search goes through `v1internal:generateContent` with a `web_search` request and an instruction to call `googleSearch`, printing one line per result as `URL || publication time || page title || <one sentence of the model's own words about that page>`. The cache key covers the model and that instruction sentence, so changing either starts a cold cache.
- The separator is a double pipe because a title may contain a single one: with one pipe, twelve queries produced one hundred and eight lines, each carrying extra separators the parser could not split; with two pipes, ten queries produced ninety lines that all parsed into four fields. Three columns are still accepted on read, because cache written before the fourth column existed still answers.
- No search endpoint exists upstream, so results always come back as model text, read in order: URL lines, `groundingMetadata` citations, then the model's prose answer as a single result. Only URL lines carry `Title`, `Date`, `SiteName` and the model's sentence in `Summary`; the other shapes are the fallback for a model that answered instead of searching.
- Do not ask the model for upstream `SearchResults` verbatim: reproducing page text trips the copyright filter, which answers `finishReason=RECITATION` with `The generated content was filtered because it may contain material that resembles existing copyrighted works.` on ordinary queries, and asking it to encode that payload makes it invent fields rather than copy them.
- A bare query (`linux`) is answered from the model's knowledge with zero `groundingChunks`, so the instruction sentence is what makes the tool call happen.
- An empty answer still renders `{"search_results":[]}` with the same `Content-Type` as a populated one, so Kimi Code reads an empty result rather than a transport error.

## Snippets

- Each result gets a snippet from `snippet.go`'s `fillSnippets`: the model's sentence by default, replaced by the page's lead paragraph when the page can be read within a two-second budget for the whole batch.
- Pages are read in parallel through `request.FetchPageText` — `localRequester.Fetch`, not the extractor directly — so a page cached by a previous fetch answers from the forward cache (3600 seconds) instead of downloading again.
- A read unfinished at budget expiry keeps the model's sentence; snippets sit in a separate slice so a late write cannot reach results the caller already has.
- Measured live: nine pages return in 2.0 to 2.1 seconds, with two to seven pages read.
- `localLeadSnippet` judges a page's first paragraph, rejecting headings, tables, lists, link-only lines and chrome such as `Skip to content` or `You signed in`, falling back to the model's sentence when nothing qualifies.

## Providers and local extraction

- `deepseek` calls its own `/v1/tools/search` and `/v1/tools/fetch`; `exa` posts to `api.exa.ai/search` and `/contents` with `x-api-key`; `codex` forwards `/backend-api/codex/alpha/search`; `local` fetches in-process, extracts to markdown, keys its cache entry on provider and URL since it builds no request of its own.
- The local extractor picks the densest of `article`, `main`, `div`, `role=main`, falling back to the whole `body` when the best holds fewer than 400 characters, so a stub article still yields full text. Tables render as markdown rows, because a flattened kernel.org changelog loses the column saying which release a line belongs to.
- The extractor skips a subtree the page keeps out of sight: `display:none` in the `style` attribute (compared with spaces and case removed), a `hidden` attribute, or `aria-hidden="true"`. A page ships its browser-upgrade notice, cookie bar and login dialog in the markup behind one of those until a script shows them, so reading the markup as written put `You are using an out of date browser` into a snippet about the page. `localNodeHidden` is the single predicate; the renderer, the content-density scan and `localNodeText` all consult it. Measured on live pages, dropping those subtrees removes the notice from `www.linux.org` and leaves the body of `bird.nic.cz`, `www.debian.org` and `go.dev` intact. This is not something a headless browser is needed for: `www.linux.org` answers Chrome 131's User-Agent with byte-identical HTML, so the notice is present in every fetch and is only ever hidden by CSS.
- A login box a page leaves visible is caught by `localPageChromeMarkers` in `snippet.go`, which is why `www.linux.com` no longer describes itself as `Sign in Welcome! Log into your account`.
- An empty result and an empty body are errors: upstreams bill only a successful answer and the caller refunds the same way.
- A result repeating another one's host and snippet is dropped after snippets are filled (`dropRepeatedSiteResults`), because a page the model listed under two of its own URLs otherwise takes two slots with one description. Only the pair is compared, so a site's homepage, documentation and blog all stay listed.
- A fetch that reaches its target and gets a non-2xx answer keeps that status: `localFetchStatusError` carries it and `request.FetchAnswerStatus` reads it, so `/moonshot.service/v1/fetch` answers a 404 target with 404 and a name that does not resolve with 502.

## Rendered results

- `num_results` on `/moonshot.service/v1/search` caps the rendered list after snippets are filled, on the rendered path only: a native candidate forwards byte for byte, so DeepSeek's `/v1/tools/*` and Codex's `/v1/alpha/search` answers are never truncated.
- `max_results` is read as the same cap through `MoonshotServiceRequest.ResultCount`, because an earlier revision of the client documentation used that name. `num_results` wins when both are present; a caller following the older name is capped rather than silently ignored.
- `date` is always present: a modeled date is normalised to ISO 8601 (`2006-01-02`), an unreadable one passes through unchanged, and no date renders `"date": null` rather than dropping the key, so a client never confuses a missing key with an empty value.
- On `/moonshot.service/v1/fetch`, a page that answered without extractable text is still 200 with an empty body, while an unreadable page is 502 with the upstream error; reporting both as empty would hide an unreachable site behind a successful answer.

## Kimi Code endpoints

`relay/moonshot_service_handler.go`, `router/moonshot_service-router.go`, `relay/constant/moonshot_service.go`.

- `/moonshot.service/v1/search` and `/moonshot.service/v1/fetch` are one group behind `TokenAuth` and `Distribute`. The client writes `https://llm.real186526.cn/moonshot.service/v1` as `base_url` and appends `/search` or `/fetch`.
- `/search` takes `{"text_query": ...}`, answers 200 with `{"search_results":[{title,url,snippet,date,site_name}]}`.
- `/fetch` takes `{"url": ...}`, answers the extracted body as `text/markdown`, not JSON, because a client reads a non-200 as a failed fetch and falls back to fetching locally.
- The distributor names the attempt `tools-search` or `tools-fetch` instead of reading a model from the body — the same pair `/v1/tools/*` assigns: the capability is what the channel serves and what the call costs, so one name covers every route into the pipeline.
- A channel able to serve a capability serves every route assigning it: a DeepSeek channel answers `/moonshot.service/v1/*` and `/v1/tools/*` alike, because the caller's path chooses the contract and the channel chooses the upstream, the renderer bridging them. That is intended, not a side effect: tying a capability name to one protocol would need one channel entry per client shape for the same upstream.

## Route handlers and billing

`relay/deepseek_tools_handler.go`, `relay/alpha_search_handler.go`, `relay/moonshot_service_handler.go`.

- All five routes go through `service/websearch`; each handler keeps only channel-specific parts — model mapping, parameter override, and the URL and headers the channel's adaptor resolves, passed in as `request.Query.Endpoint` and `Prepare` so the pipeline never learns a channel type.
- A channel no candidate can serve answers `ErrUnsupportedChannel`, which the relay loop may retry on another channel.
- `/v1/tools/*` settles one call when the answer carries results or page content and refunds otherwise; `/v1/alpha/search` bills one `web_search_preview` call; the Kimi Code endpoints refund an empty answer and still answer 200 with an empty body, because their client treats any non-200 as a failed fetch.
- A non-2xx pipeline answer is rebuilt into an `*http.Response` and handed to `service.RelayErrorHandler`, keeping `status_code_mapping` and message rewriting in one place.
- `relay/legacy_websearch_parity_test.go`, `relay/legacy_alpha_search_parity_test.go` and `relay/websearch_pipeline_test.go` pin the byte-for-byte forward, the rendered answer, the skip and stop rules, and the cache.
- `relay/moonshot_service_handler_test.go` pins the Kimi Code half: a hit settles 5000 and a fetch 1000, an empty answer refunds and still answers 200, and a failed fetch answers an empty page so the client's local fallback is not double-charged.
- `service/websearch/request/transport_test.go` pins which statuses are worth a retry (408, 429, 5xx) and the three levels a cache lifetime resolves through (request option, channel setting, built-in default, negative disabling). `service/text_quota_test.go`'s `TestSearchCapabilitySettlesOncePerCall` runs the same reservation and settlement on sqlite for the two per-call prices, including the refund path of an empty answer.

## Global settings and pricing

`setting/websearch_setting/` holds the global defaults, registered with `config.GlobalConfig` so `model/option.go`'s `handleConfigUpdate` applies a saved `websearch_setting.*` option without a restart.

- Keys: `candidate_order` (comma separated provider names, unknown names dropped), `native_first`, the three cache TTLs, `response_header_timeout_seconds`, `retry_attempts`, `retry_base_delay_milliseconds`, `exa_base_url`, `exa_api_key`, `antigravity_search_model`. A non-zero channel setting wins over the global value; a zero here keeps the built-in default; a negative cache TTL disables that cache.
- `Get()` publishes an `atomic.Value` snapshot, so a reader copies once and cannot see a half-applied update. `ProviderOrder()` is the only splitter of `candidate_order`; `CandidateOrder` never adds or removes a candidate.
- `exa_api_key` is the one credential here: `controller/option.go`'s `GetOptions` drops any key ending in `api_key`, so the admin page reads `ExaKeyConfigured` instead. Only a non-empty `exa_api_key` makes Exa a candidate on every channel; an empty one keeps the cost at zero for a deployment that never asked for it.
- `setting/billing_setting/builtin_billing.go` carries the per-request prices of `tools-search` and `tools-fetch`, the only two models billed per call instead of by token: `tier("request", fixed(...))` charges once whatever the request holds, and `billing_setting.billing_expr` overrides both. Both names are capabilities, not protocols — `/v1/tools/*` and `/moonshot.service/v1/*` assign them — so the price covers the most expensive upstream any candidate can reach, which is why a search costs more than a fetch.

## Model square and stored names

- `common/endpoint_type.go`: `GetEndpointTypesByChannelType` decides which endpoint the model square shows. `tools-search` and `tools-fetch` map to every endpoint speaking them — the DeepSeek tools route and the Moonshot Service route for each; `jev` maps to System One alone, not being served through the chat endpoints.
- `model/search_capability_migration.go` renames stored routing names at server start: a channel's `models` column and a token's `model_limits` still holding `deepseek-search`, `moonshot-search`, `deepseek-fetch` or `moonshot-fetch` become `tools-search` or `tools-fetch`, and the abilities of every channel touched are rebuilt in the same transaction. An already-current name matches nothing, so the migration reruns harmlessly.
