# System One Rules

The TypeSafe System One contract, served at `/systemone/v1/*` and the `/v1/systemone` alias. Files: `relay/systemone_handler.go`, `relay/systemone_validate.go`, `relay/systemone_upstream_error.go`, `relaykit/systemone/`, `relaykit/dto/systemone_request.go`, `relay/common/systemone_path.go`, `relay/constant/systemone.go`, `relay/helper/valid_request.go`, `relay/common/relay_info.go`, `relay/constant/relay_mode.go`, `constant/systemone.go`, `common/error_shape.go`, `common/typesafe_error.go`, `common/media_type.go`, `common/systemone_model.go`, `middleware/systemone_shape.go`, `middleware/systemone_body_limit.go`, `middleware/systemone_model_guard.go`, `router/systemone-router.go`, `router/relay-router.go`, `controller/relay.go`, `controller/systemone_models.go`, `model/model_descriptions.go`.

## Contract

- TypeSafe System One contract for decision models: `/systemone/v1/models`, `/systemone/v1/systemone`; `/v1/systemone` is an alias of the same contract.
- Errors use the upstream document family `{"detail": ...}` — array of `{loc,msg,type}` for a rejected body, object with `error_type` and `message` otherwise. `middleware.SystemOneErrorShape` sets the shape before authentication; the `/v1` group carries it so the alias is shaped before `TokenAuth` can refuse a key.
- Schema check (`relay/systemone_validate.go`): required `model`/`state`/`questions`; `state` as string/object/array; `choice.criteria` 1–255 keys; `score.criteria` 1–10 levels; `noul` needs `criteria` or `instructions`; nesting ≤ 64; finite numbers; 64k/32k context budgets raised to 262k by `TypeSafe-Danger-Zone-Long-Context`; `TypeSafe-Danger-Zone-Images` required for `assets` or a `box` question. Rejection answers 422 with the array.
- Multipart folded into JSON first (`relaykit/systemone/multipart.go`): the `request` part is the document; other parts become `data_base64` and `format` of the asset naming them; an unused part or dangling reference is a 422.
- Body over 32 MiB answers 413 before it is read. Non-`jev` model answers 400 before channel selection; without that guard the no-channel path reports a client mistake as 500 `get_channel_failed`.
- Contract prefix goes upstream as `/v1/systemone`, the only path served (`relay/common/systemone_path.go`), query string kept.
- Upstream 400/401/403/404/413/415/422/429 keeps its own status and document with retry skipped, so a deterministic refusal is never counted against the channel; 5xx stays with `service.RelayErrorHandler` so a transient fault retries.
- `GET /systemone/v1/models` answers `{"models":[{name,description,release_date}]}` for the caller's own groups, `jev` models only. No single-model endpoint exists upstream, so `/systemone/v1/models/:name` is a contract-shaped 404.
- Response keeps its answers, reports the requested model name, drops the provider's `opencode` account block, settles on `usage.input_tokens` / `output_tokens`.
- `GET /v1/models/<unknown>` answers 404, not 200 with an error document.
- `middleware/gzip.go` reads `Content-Encoding` case-insensitively, treats `x-gzip` as `gzip`, refuses unknown or stacked coding with 415. `Content-Type` is compared after `mime.ParseMediaType` (`common/media_type.go`), not by case-sensitive prefix.

