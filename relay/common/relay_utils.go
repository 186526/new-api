package common

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

type HasPrompt interface {
	GetPrompt() string
}

type HasImage interface {
	HasImage() bool
}

func GetFullRequestURL(baseURL string, requestURL string, channelType int) string {
	fullRequestURL := fmt.Sprintf("%s%s", baseURL, requestURL)

	if strings.HasPrefix(baseURL, "https://gateway.ai.cloudflare.com") {
		switch channelType {
		case constant.ChannelTypeOpenAI:
			fullRequestURL = fmt.Sprintf("%s%s", baseURL, strings.TrimPrefix(requestURL, "/v1"))
		case constant.ChannelTypeAzure:
			fullRequestURL = fmt.Sprintf("%s%s", baseURL, strings.TrimPrefix(requestURL, "/openai/deployments"))
		}
	}
	return fullRequestURL
}

func SanitizeURLForLog(rawURL string) string {
	if rawURL == "" {
		return rawURL
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}

	query := parsedURL.Query()
	if len(query) == 0 {
		return rawURL
	}

	changed := false
	for key := range query {
		if isSensitiveURLQueryKey(key) {
			query.Set(key, "***masked***")
			changed = true
		}
	}
	if !changed {
		return rawURL
	}

	parsedURL.RawQuery = query.Encode()
	return parsedURL.String()
}

func isSensitiveURLQueryKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	switch normalized {
	case "key",
		"api_key",
		"api-key",
		"apikey",
		"x-api-key",
		"access",
		"access_token",
		"refresh_token",
		"id_token",
		"token",
		"authorization",
		"auth",
		"client_secret",
		"secret",
		"password",
		"passwd",
		// Payment callbacks: Epay signs with sign and sends its merchant id
		// beside it. Both stay masked on an upstream debug URL too; a channel
		// that names its own auth query sign or pid then loses a readable
		// value, which fails closed.
		"sign",
		"pid",
		"signature",
		"sig",
		"awsaccesskeyid",
		"x-amz-credential",
		"x-amz-security-token",
		"x-amz-signature":
		return true
	}
	return strings.Contains(normalized, "token") ||
		strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "signature")
}

// paymentCallbackQueryKeyNames names the signature keys the payment gateways
// known to this tree use. A key the list does not hold can still be a
// signature, which is why the access log also recognises a callback by its
// path; this list only widens the net for a callback logged under another
// path, such as the signed redirect to a frontend return page.
var paymentCallbackQueryKeyNames = map[string]bool{
	"pid":        true,
	"sig":        true,
	"hash":       true,
	"hmac":       true,
	"digest":     true,
	"epay_sign":  true,
	"caller_key": true,
}

// paymentSignatureNameSegments are the words a separated key segment can be
// without being a payment signature. Read alongside paymentCallbackQueryKeyNames,
// they let a name be judged word by word instead of by substring, so `design`
// and `signal` keep their value in the log.
var paymentSignatureNameSegments = map[string]bool{
	"sign":   true,
	"sig":    true,
	"hmac":   true,
	"hash":   true,
	"digest": true,
}

// IsPaymentCallbackPath reports whether a path is one this tree serves a
// payment callback on. The decision reads only the path: the route names come
// from new-api's own router, so the gateway cannot move a callback out of this
// rule by naming its signature parameter something else. A custom notify_url
// changes the scheme, host and any path prefix before the route, so the rule
// reads every path segment instead of matching the registered path whole.
//
// A false positive only drops one log line's query; the handler still reads
// the query it was sent.
func IsPaymentCallbackPath(path string) bool {
	if path == "" {
		return false
	}
	segments := strings.Split(strings.TrimSuffix(path, "/"), "/")
	for _, segment := range segments {
		if segment == "webhook" {
			return true
		}
	}
	switch segments[len(segments)-1] {
	case "notify", "return":
		// The Epay callback and its signed redirect are the routes that end
		// this way; both carry the signature in the query.
		return true
	}
	return false
}

// IsPaymentCallbackQueryName reports whether one query key names a payment
// signature. The name is read in segments, so `provider_sign` and
// `merchantSign` count while `design` and `signal` do not: a page keeps its
// readable query unless a segment really is a signature word.
func IsPaymentCallbackQueryName(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if normalized == "" {
		return false
	}
	if paymentCallbackQueryKeyNames[normalized] {
		return true
	}
	for _, segment := range strings.FieldsFunc(normalized, func(r rune) bool {
		return r == '_' || r == '-' || r == '.'
	}) {
		if paymentSignatureNameSegments[segment] {
			return true
		}
	}
	// A camelCase name has no separator, so read the suffixes too, but only at a
	// word boundary: `merchantSign` counts and `design` does not.
	for _, suffix := range []string{"signature", "hmac", "sign", "sig"} {
		if !strings.HasSuffix(normalized, suffix) {
			continue
		}
		start := len(normalized) - len(suffix)
		if start == 0 {
			return true
		}
		rawSuffix := key[len(key)-len(suffix):]
		if rawSuffix[0] >= 'A' && rawSuffix[0] <= 'Z' {
			return true
		}
		if !isASCIILetter(normalized[start-1]) {
			return true
		}
	}
	return false
}

func isASCIILetter(character byte) bool {
	return (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z')
}

// IsPaymentCallbackQueryText reports whether raw query text carries a payment
// signature key. It is the same net as IsPaymentCallbackQuery for a query that
// arrived as text, as one does when a path parameter swallowed the "?" and the
// router resolved the query into the path.
func IsPaymentCallbackQueryText(rawQuery string) bool {
	if rawQuery == "" {
		return false
	}
	for part := range strings.SplitSeq(rawQuery, "&") {
		rawKey, _, _ := strings.Cut(part, "=")
		key, err := url.QueryUnescape(rawKey)
		if err != nil {
			key = rawKey
		}
		if IsPaymentCallbackQueryName(key) {
			return true
		}
	}
	return false
}

func GetAPIVersion(c *gin.Context) string {
	query := c.Request.URL.Query()
	apiVersion := query.Get("api-version")
	if apiVersion == "" {
		apiVersion = c.GetString("api_version")
	}
	return apiVersion
}

func createTaskError(err error, code string, statusCode int, localError bool) *dto.TaskError {
	return &dto.TaskError{
		Code:       code,
		Message:    err.Error(),
		StatusCode: statusCode,
		LocalError: localError,
		Error:      err,
	}
}

func storeTaskRequest(c *gin.Context, info *RelayInfo, action string, requestObj TaskSubmitReq) {
	info.Action = action
	c.Set("task_request", requestObj)
}
func GetTaskRequest(c *gin.Context) (TaskSubmitReq, error) {
	v, exists := c.Get("task_request")
	if !exists {
		return TaskSubmitReq{}, fmt.Errorf("request not found in context")
	}
	req, ok := v.(TaskSubmitReq)
	if !ok {
		return TaskSubmitReq{}, fmt.Errorf("invalid task request type")
	}
	return req, nil
}

func validatePrompt(prompt string) *dto.TaskError {
	if strings.TrimSpace(prompt) == "" {
		return createTaskError(fmt.Errorf("prompt is required"), "invalid_request", http.StatusBadRequest, true)
	}
	return nil
}

// MaxTaskDurationSeconds caps user-supplied video duration. Duration is used
// as a billing multiplier (OtherRatio "seconds"); an unbounded value could
// overflow quota calculation into a negative charge.
const MaxTaskDurationSeconds = 3600

func validateTaskDurationBounds(req TaskSubmitReq) *dto.TaskError {
	seconds := req.Duration
	if seconds == 0 && req.Seconds != "" {
		seconds, _ = strconv.Atoi(req.Seconds)
	}
	if seconds < 0 || seconds > MaxTaskDurationSeconds {
		return createTaskError(fmt.Errorf("seconds must be between 1 and %d", MaxTaskDurationSeconds), "invalid_seconds", http.StatusBadRequest, true)
	}
	return nil
}

func validateMultipartTaskRequest(c *gin.Context, info *RelayInfo, action string) (TaskSubmitReq, error) {
	var req TaskSubmitReq
	if _, err := c.MultipartForm(); err != nil {
		return req, err
	}

	formData := c.Request.PostForm
	req = TaskSubmitReq{
		Prompt:   formData.Get("prompt"),
		Model:    formData.Get("model"),
		Mode:     formData.Get("mode"),
		Image:    formData.Get("image"),
		Size:     formData.Get("size"),
		Metadata: make(map[string]any),
	}

	if durationStr := formData.Get("seconds"); durationStr != "" {
		if duration, err := strconv.Atoi(durationStr); err == nil {
			req.Duration = duration
		}
	}

	if images := formData["images"]; len(images) > 0 {
		req.Images = images
	}

	for key, values := range formData {
		if len(values) > 0 && !isKnownTaskField(key) {
			if intVal, err := strconv.Atoi(values[0]); err == nil {
				req.Metadata[key] = intVal
			} else if floatVal, err := strconv.ParseFloat(values[0], 64); err == nil {
				req.Metadata[key] = floatVal
			} else {
				req.Metadata[key] = values[0]
			}
		}
	}
	return req, nil
}

func ValidateMultipartDirect(c *gin.Context, info *RelayInfo) *dto.TaskError {
	var prompt string
	var model string
	var seconds int
	var size string
	var hasInputReference bool

	var req TaskSubmitReq
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		return createTaskError(err, "invalid_json", http.StatusBadRequest, true)
	}

	prompt = req.Prompt
	model = req.Model
	size = req.Size
	seconds, _ = strconv.Atoi(req.Seconds)
	if seconds == 0 {
		seconds = req.Duration
	}
	if req.InputReference != "" {
		req.Images = []string{req.InputReference}
	} else if len(req.Images) == 0 && strings.TrimSpace(req.Image) != "" {
		// 兼容单图上传
		req.Images = []string{strings.TrimSpace(req.Image)}
	}

	if strings.TrimSpace(req.Model) == "" {
		return createTaskError(fmt.Errorf("model field is required"), "missing_model", http.StatusBadRequest, true)
	}

	if req.HasImage() {
		hasInputReference = true
	}

	if taskErr := validatePrompt(prompt); taskErr != nil {
		return taskErr
	}

	if taskErr := validateTaskDurationBounds(req); taskErr != nil {
		return taskErr
	}

	action := constant.TaskActionTextToVideo
	if hasInputReference {
		action = constant.TaskActionImageToVideo
	}
	if strings.HasPrefix(model, "sora-2") {

		if size == "" {
			size = "720x1280"
		}

		if seconds <= 0 {
			seconds = 4
		}

		if model == "sora-2" && !lo.Contains([]string{"720x1280", "1280x720"}, size) {
			return createTaskError(fmt.Errorf("sora-2 size is invalid"), "invalid_size", http.StatusBadRequest, true)
		}
		if model == "sora-2-pro" && !lo.Contains([]string{"720x1280", "1280x720", "1792x1024", "1024x1792"}, size) {
			return createTaskError(fmt.Errorf("sora-2 size is invalid"), "invalid_size", http.StatusBadRequest, true)
		}
		// OtherRatios 已移到 Sora adaptor 的 EstimateBilling 中设置
	}

	storeTaskRequest(c, info, action, req)

	return nil
}

func isKnownTaskField(field string) bool {
	knownFields := map[string]bool{
		"prompt":          true,
		"model":           true,
		"mode":            true,
		"image":           true,
		"images":          true,
		"size":            true,
		"duration":        true,
		"input_reference": true, // Sora 特有字段
	}
	return knownFields[field]
}

func ValidateBasicTaskRequest(c *gin.Context, info *RelayInfo, action string) *dto.TaskError {
	var err error
	contentType := c.GetHeader("Content-Type")
	var req TaskSubmitReq
	if strings.HasPrefix(contentType, "multipart/form-data") {
		req, err = validateMultipartTaskRequest(c, info, action)
		if err != nil {
			return createTaskError(err, "invalid_multipart_form", http.StatusBadRequest, true)
		}
	}
	// 为了metadata字段的兼容性，统一UnmarshalBodyReusable
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		return createTaskError(err, "invalid_request", http.StatusBadRequest, true)
	}

	if taskErr := validatePrompt(req.Prompt); taskErr != nil {
		return taskErr
	}

	if taskErr := validateTaskDurationBounds(req); taskErr != nil {
		return taskErr
	}

	if len(req.Images) == 0 && strings.TrimSpace(req.Image) != "" {
		// 兼容单图上传
		req.Images = []string{req.Image}
	}

	storeTaskRequest(c, info, action, req)
	return nil
}
