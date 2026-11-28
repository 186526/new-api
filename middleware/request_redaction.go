package middleware

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service/redaction"
	"github.com/QuantumNous/new-api/setting/redaction_setting"
	"github.com/gin-gonic/gin"
)

const requestRedactionLogPath = "request_redaction_log_path"
const responsesWSFrameKey = "request_redaction_responses_ws_frame"

func MarkResponsesWSFrame(ctx *gin.Context) {
	ctx.Set(responsesWSFrameKey, true)
}

func enforceRequestRedaction(ctx *gin.Context, authenticated *model.Token) bool {
	guardrails := redaction_setting.Get()
	if redactionBodylessRoute(ctx) {
		return true
	}
	// Consult policy metadata on the request path; an older signed auth-cache
	// entry must not let a just-enabled policy forward the original body.
	stored, err := model.GetTokenRedaction(authenticated.Id)
	if err != nil {
		ctx.Set(requestRedactionLogPath, ctx.FullPath())
		return rejectRequestRedaction(ctx, http.StatusServiceUnavailable, "Request redaction settings are unavailable.")
	}
	if !stored.RedactionEnabled {
		return true
	}
	ctx.Set(requestRedactionLogPath, ctx.FullPath())
	if !guardrails.Enabled {
		return rejectRequestRedaction(ctx, http.StatusServiceUnavailable, "Request redaction is disabled by the administrator.")
	}
	format := requestRedactionFormat(ctx)
	responsesHandshake := ctx.Request.Method == http.MethodGet && ctx.FullPath() == "/v1/responses" && strings.EqualFold(ctx.GetHeader("Upgrade"), "websocket")
	if format == "" && !responsesHandshake {
		return rejectRequestRedaction(ctx, http.StatusForbidden, "This endpoint cannot enforce request redaction.")
	}
	if authenticated.RedactionPolicyVersion() == stored.RedactionPolicyVersion() {
		stored = *authenticated
	}
	stored.Key = authenticated.Key
	policy, restoreRequested, err := compileTokenRedaction(stored, guardrails)
	if err != nil {
		return rejectRequestRedaction(ctx, http.StatusServiceUnavailable, "Request redaction settings are invalid.")
	}
	if restoreRequested && (!policy.RestoresPII() || !redaction_setting.ResponseRestoreReady() || !supportsResponseRestore(ctx, format, responsesHandshake)) {
		return rejectRequestRedaction(ctx, http.StatusForbidden, "This endpoint cannot safely restore redacted response text.")
	}
	if responsesHandshake {
		// The request runner rechecks policy and rewrites the JSON on every create.
		return true
	}
	session, err := policy.NewSession(stored.RedactionFingerprintKey())
	if err != nil {
		return rejectRequestRedaction(ctx, http.StatusServiceUnavailable, "Request redaction is unavailable.")
	}
	return rewriteProtectedRequest(ctx, session, format)
}

func requestRedactionFormat(ctx *gin.Context) redaction.Format {
	if ctx.Request.Method != http.MethodPost || ctx.GetHeader("Upgrade") != "" {
		return ""
	}
	switch ctx.FullPath() {
	case "/v1/chat/completions":
		return redaction.Chat
	case "/v1/responses", "/v1/responses/compact":
		return redaction.Responses
	case "/v1/tools/search", "/v1/alpha/search", "/moonshot.service/v1/search":
		return redaction.Search
	case "/v1/tools/fetch", "/moonshot.service/v1/fetch":
		return redaction.Fetch
	case "/v1/messages":
		return redaction.Claude
	case "/v1beta/models/*path", "/v1/models/*path":
		path := ctx.Param("path")
		if strings.HasSuffix(path, ":generateContent") || strings.HasSuffix(path, ":streamGenerateContent") || strings.HasSuffix(path, ":batchGenerateContent") {
			return redaction.Gemini
		}
	}
	return ""
}

func redactionBodylessRoute(ctx *gin.Context) bool {
	if ctx.Request.Method != http.MethodGet || ctx.GetHeader("Upgrade") != "" {
		return false
	}
	switch ctx.FullPath() {
	case "/v1/models", "/v1/models/:model", "/v1beta/models", "/v1beta/openai/models", "/systemone/v1/models", "/systemone/v1/models/:name":
		return true
	}
	return false
}

func rewriteProtectedRequest(ctx *gin.Context, session *redaction.Session, format redaction.Format) bool {
	for _, key := range []string{common.KeyRequestBody, common.KeyBodyStorage, gin.BodyBytesKey} {
		if value, exists := ctx.Get(key); exists && value != nil {
			return rejectRequestRedaction(ctx, http.StatusServiceUnavailable, "Request redaction must run before request storage.")
		}
	}
	request := ctx.Request
	if !sanitizeProtectedTarget(request, session, format) {
		return rejectRequestRedaction(ctx, http.StatusBadRequest, "Request target cannot be redacted safely.")
	}
	if !common.IsJSONMediaType(common.MediaTypeOf(request.Header.Get("Content-Type"))) || request.Body == nil || request.Form != nil || request.MultipartForm != nil {
		return rejectRequestRedaction(ctx, http.StatusUnsupportedMediaType, "Request redaction requires a JSON body.")
	}
	for _, coding := range normalizeContentCoding(strings.Join(request.Header.Values("Content-Encoding"), ",")) {
		if coding != "identity" {
			return rejectRequestRedaction(ctx, http.StatusUnsupportedMediaType, "Request redaction requires a decoded JSON body.")
		}
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, redaction.MaxBodyBytes+1))
	_ = request.Body.Close()
	if len(body) > redaction.MaxBodyBytes || common.IsRequestBodyTooLargeError(err) {
		return rejectRequestRedaction(ctx, http.StatusRequestEntityTooLarge, "Request exceeds the redaction processing limit.")
	}
	if err != nil {
		return rejectRequestRedaction(ctx, http.StatusBadRequest, "Request redaction could not read the body.")
	}
	rewritten, err := session.RewriteJSON(body, format)
	if errors.Is(err, redaction.ErrLimit) {
		return rejectRequestRedaction(ctx, http.StatusRequestEntityTooLarge, "Request exceeds the redaction processing limit.")
	}
	if err != nil {
		return rejectRequestRedaction(ctx, http.StatusBadRequest, "Request body cannot be redacted safely.")
	}
	request.Body = io.NopCloser(bytes.NewReader(rewritten))
	request.GetBody = nil
	request.ContentLength = int64(len(rewritten))
	request.Header = http.Header{"Content-Type": {"application/json"}, "Content-Length": {strconv.Itoa(len(rewritten))}}
	request.Trailer = nil
	request.TransferEncoding = nil
	ctx.Request = request.WithContext(session.ProtectedContext(request.Context()))
	ctx.Set(responseRedactionSessionKey, session)
	return true
}

func sanitizeProtectedTarget(request *http.Request, session *redaction.Session, format redaction.Format) bool {
	target := request.URL
	if target == nil || target.IsAbs() || target.Host != "" || target.User != nil || target.Opaque != "" || target.Fragment != "" {
		return false
	}
	if format == redaction.Gemini && session.CheckURLComponent(target.EscapedPath()) != nil {
		return false
	}
	query, err := url.ParseQuery(target.RawQuery)
	if err != nil {
		return false
	}
	for name, values := range query {
		if len(values) != 1 {
			return false
		}
		switch {
		case format == redaction.Gemini && name == "key":
			query.Del(name) // TokenAuth has already consumed the downstream credential.
		case format == redaction.Gemini && name == "alt" && values[0] == "sse":
		case format == redaction.Claude && name == "beta" && values[0] == "true":
		default:
			return false
		}
	}
	target.RawQuery = query.Encode()
	target.ForceQuery = false
	request.RequestURI = target.RequestURI()
	return true
}

func requestIsRedacted(ctx *gin.Context) bool {
	return redaction.IsProtectedRequest(ctx.Request.Context())
}

func rejectRequestRedaction(ctx *gin.Context, status int, message string) bool {
	abortWithOpenAiMessage(ctx, status, message, types.ErrorCode("request_redaction_failed"))
	return false
}
