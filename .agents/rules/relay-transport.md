# Relay Transport Rules

Conventions for the HTTP transport between the relay and upstream providers: stream keepalive, tiered header wait, log redaction of payment signatures, the frontend/backend asset split, and relay metrics.

## Stream keepalive

Files: `relay/helper/common.go`, `relay/helper/stream_scanner.go`, `relay/channel/api_request.go`, `relay/channel/openai/relay-openai.go`.

- `doRequest` starts the keepalive only after `client.Do` returns a 2xx, and the first heartbeat goes out immediately. The response headers therefore stay uncommitted while an attempt can still fail, so the client receives the real 429/504 instead of an error event inside a 200 stream.
- Later heartbeats use `general_setting.ping_interval_seconds` (default 5s).
- `streamKeepAliveWriter.WriteHeader` keeps the caller's status until a heartbeat has committed the headers.
- `OaiStreamHandler` copies the forwarded upstream headers before the scanner runs, because the headers are already committed by then.

## Tiered upstream header wait

Files: `service/http_transport_policy.go`, `service/http_transport_sharded.go`, `common/constants.go`.

- `response_header_timeout_seconds` (or `RELAY_RESPONSE_HEADER_TIMEOUT`) covers the first attempt; `response_header_timeout_retry_seconds` (or `RELAY_RESPONSE_HEADER_TIMEOUT_RETRY`) covers later attempts. The total silence of a retried request therefore stays inside the edge timeout.
- `applyHTTPTransportPolicy` also sets `HTTP2.SendPingTimeout` (15s) and `HTTP2.PingTimeout` (5s), so an idle HTTP/2 connection is probed and a dead one is dropped instead of parking every multiplexed request until the header timeout.

## Payment signatures never reach the logs

Files: `middleware/logger.go`, `middleware/recover.go`, `relay/common/relay_utils.go`.

- `redactRequestTarget` drops the whole query of a payment callback, recognised by the route (`IsPaymentCallbackPath`) or by a signature key name in the query (`IsPaymentCallbackQueryText`).
- Both predicates run a second time on a target split at the `=` of a path segment. That is the shape an escaped query takes when it is decoded into the path as `…/notify/sign=SECRET&pid=ID` with no `?` and an empty `RawQuery`.
- `splitQueryDecodedIntoPath` walks the path's segments forward once and tries every `=`-bearing one, not only the first, because an earlier segment may carry a decoded `=` of its own (`/api/user/epay/x=y/notify/sign=SECRET`). It returns the first split whose route half still ends in the callback segment or whose query half starts with a signature key name.
- Each candidate hands the key-name predicate only its own segment, so the cost is linear in the request line however it is shaped. Measured with `BenchmarkRedactRequestTarget`, an `&`-dense 8 KiB path costs tens of microseconds and a single 8 KiB `=` segment a few hundred nanoseconds.
- It stops after `paymentSplitSegmentLimit` (64) `=`-bearing segments, which bounds only how many candidates a crafted path can create, and then logs the first candidate. A real callback URL has a handful of segments, so the ceiling only binds on a crafted path, and the fallback route half ends before the first `=` and drops log detail rather than a value.
- A double-encoded `=` (`%253D`) is a known limit: it decodes once to `%3D`, so the path holds no literal `=` and the value stays percent-encoded in the log, recoverable by decoding the line.
- `PanicRecover` writes the same redacted target instead of gin's request dump.

## Frontend/backend asset split

Files: `web_assets_embedded.go`, `web_assets_standalone.go`, `router/main.go`, `Dockerfile`, `docker/web-nginx.conf`.

- The default build embeds `web/dist` and serves the dashboard itself.
- `-tags no_web_assets` leaves the asset tree out and answers non-API paths with 404.
- The `web` image serves `web/dist` through nginx with immutable caching for `/static` and a client-route fallback to `index.html`.
- The analytics injection in `main.go` only runs in the embedded build, so a deployment that moves to the web image must inject analytics in its own frontend pipeline.

## Relay and upstream-attempt metrics

Files: `controller/relay.go`, `common/gin.go`, `pkg/metrics/relay.go`.

- The request body size comes from the bytes `GetBodyStorage` recorded (`common.GetRequestBodyBytes`), because a streamed request reports `Content-Length: -1` and would be sized from nothing.
- The histogram buckets are whole kilobytes, so the `le` label reads as an exact size.

## WebSocket username label

Files: `relay/responses_websocket.go`.

- WebSocket relays carry a username label.
