package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/redaction"
	"github.com/QuantumNous/new-api/setting/redaction_setting"
	"github.com/gin-gonic/gin"
)

func tokenRedactionGuardrails() gin.H {
	settings := redaction_setting.Get()
	readiness := redaction_setting.Readiness()
	return gin.H{
		"available":                readiness.EnableKeys && settings.Enabled,
		"max_custom_rules_per_key": settings.MaxCustomRulesPerKey,
		"allow_response_restore":   settings.Enabled && settings.AllowResponseRestore && readiness.ConfigureRestore,
		"runtime":                  readiness.Runtime(),
	}
}

var errRedactionUnavailable = errors.New("request redaction is unavailable")
var errRedactionDisabled = errors.New("request redaction is disabled by the administrator")

func redactionRestorePolicyError() error {
	return &redaction.FieldError{Field: "restore_pii_in_response", Reason: "response restoration is disabled for this policy", MessageKey: "redaction.restore_unavailable"}
}

func GetTokenRedactionGuardrails(ctx *gin.Context) {
	common.ApiSuccess(ctx, tokenRedactionGuardrails())
}

type tokenRedactionInput struct {
	RedactionEnabled  *bool             `json:"redaction_enabled"`
	RedactionConfig   *redaction.Config `json:"redaction_config"`
	RedactionRevision *int64            `json:"redaction_revision"`
}

func (input tokenRedactionInput) submitted() bool {
	return input.RedactionEnabled != nil || input.RedactionConfig != nil
}

type tokenRedactionSummary struct {
	BuiltinRules int              `json:"builtin_rules"`
	CustomRules  int              `json:"custom_rules"`
	Scopes       redaction.Scopes `json:"scopes"`
}

func summarizeTokenRedaction(token *model.Token) *tokenRedactionSummary {
	if !token.RedactionEnabled && token.RedactionConfig == "" {
		return nil
	}
	stored, err := token.RedactionConfigBytes()
	if err != nil {
		return nil
	}
	cfg, err := redaction.DecodeConfig(stored)
	if err != nil {
		return nil
	}
	return &tokenRedactionSummary{len(cfg.BuiltinRules), len(cfg.CustomRules), cfg.Scopes}
}

func tokenRedactionConflict(ctx *gin.Context) {
	ctx.JSON(http.StatusConflict, gin.H{"success": false, "code": "redaction_revision_conflict", "message": model.ErrTokenRedactionConflict.Error()})
}

func (input tokenRedactionInput) matchesRevision(token *model.Token) bool {
	return input.RedactionRevision != nil && *input.RedactionRevision == token.RedactionRevision
}

func applyTokenRedaction(token *model.Token, request tokenRedactionInput) (bool, error) {
	if request.RedactionEnabled == nil && request.RedactionConfig == nil {
		return false, nil
	}
	enabled := token.RedactionEnabled
	if request.RedactionEnabled != nil {
		enabled = *request.RedactionEnabled
	}
	settings := redaction_setting.Get()
	readiness := redaction_setting.Readiness()
	if enabled && !token.RedactionEnabled && !readiness.EnableKeys {
		return false, errRedactionUnavailable
	}
	if enabled && !token.RedactionEnabled && !settings.Enabled {
		return false, errRedactionDisabled
	}
	if request.RedactionConfig == nil && enabled == token.RedactionEnabled {
		return false, nil
	}
	if request.RedactionConfig == nil && !enabled {
		token.RedactionEnabled = false
		return true, nil
	}
	stored, err := token.RedactionConfigBytes()
	if err != nil && request.RedactionConfig == nil {
		return false, redaction.ErrInvalidDocument
	}
	cfg := redaction.DefaultConfig()
	if request.RedactionConfig != nil {
		cfg = *request.RedactionConfig
	} else if cfg, err = redaction.DecodeConfig(stored); err != nil {
		return false, err
	}
	if cfg.CustomRules == nil {
		cfg.CustomRules = []redaction.CustomRule{}
	}
	if cfg.Allowlist == nil {
		cfg.Allowlist = []string{}
	}
	if cfg.RestorePII && (!settings.Enabled || !settings.AllowResponseRestore || !readiness.ConfigureRestore || cfg.Strategy != "typed_placeholder") {
		return false, redactionRestorePolicyError()
	}
	if _, err := redaction.Compile(cfg, settings.MaxCustomRulesPerKey, settings.AllowResponseRestore); err != nil {
		return false, err
	}
	encoded, err := common.Marshal(cfg)
	if err != nil || len(encoded) > redaction.MaxPolicyBytes {
		return false, redaction.ErrLimit
	}
	configChanged := string(stored) != string(encoded)
	if configChanged {
		if err := token.StoreRedactionConfig(encoded); err != nil {
			return false, redaction.ErrInvalidDocument
		}
	}
	changed := token.RedactionEnabled != enabled || configChanged
	token.RedactionEnabled = enabled
	return changed, nil
}

type batchTokenRedactionItem struct {
	ID int `json:"id"`
	tokenRedactionInput
}

type batchTokenRedactionResult struct {
	ID         int    `json:"id"`
	Success    bool   `json:"success"`
	Code       string `json:"code,omitempty"`
	Field      string `json:"field,omitempty"`
	Message    string `json:"message,omitempty"`
	MessageKey string `json:"message_key,omitempty"`
	Revision   *int64 `json:"redaction_revision,omitempty"`
}

func BatchUpdateTokenRedaction(ctx *gin.Context) {
	ctx.Header("Cache-Control", "no-store")
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, 256<<10)
	var request struct {
		Items []batchTokenRedactionItem `json:"items"`
	}
	if err := ctx.ShouldBindJSON(&request); err != nil || len(request.Items) == 0 || len(request.Items) > 100 {
		rejectTokenRedaction(ctx, redaction.ErrInvalidDocument)
		return
	}
	ids := make(map[int]int, len(request.Items))
	for _, item := range request.Items {
		ids[item.ID]++
	}
	userID := ctx.GetInt("id")
	results := make([]batchTokenRedactionResult, 0, len(request.Items))
	updated := 0
	for _, item := range request.Items {
		var result batchTokenRedactionResult
		if ids[item.ID] != 1 {
			result = batchTokenRedactionResult{ID: item.ID, Code: "duplicate_id"}
		} else {
			result = updateBatchTokenRedaction(userID, item)
		}
		if result.Success {
			updated++
		}
		results = append(results, result)
	}
	params := tokenAuditParams(ctx)
	params["count"], params["updated"] = len(results), updated
	common.ApiSuccess(ctx, gin.H{"items": results, "updated": updated})
}

func updateBatchTokenRedaction(userID int, item batchTokenRedactionItem) batchTokenRedactionResult {
	result := batchTokenRedactionResult{ID: item.ID}
	if item.ID <= 0 || !item.submitted() {
		result.Code = "invalid_item"
		return result
	}
	token, err := model.GetTokenByIds(item.ID, userID)
	if err != nil {
		result.Code = "token_unavailable"
		return result
	}
	if !item.matchesRevision(token) {
		result.Code = "redaction_revision_conflict"
		return result
	}
	if _, err := applyTokenRedaction(token, item.tokenRedactionInput); err != nil {
		result.Code, result.Message = "invalid_policy", err.Error()
		result.MessageKey, result.Field = tokenRedactionErrorDetails(err)
		return result
	}
	if err := token.UpdateRedactionPolicy(); err != nil {
		result.Code = "policy_unavailable"
		if errors.Is(err, model.ErrTokenRedactionConflict) {
			result.Code = "redaction_revision_conflict"
		}
		return result
	}
	result.Success, result.Revision = true, &token.RedactionRevision
	return result
}

func tokenRedactionErrorDetails(err error) (string, string) {
	var fieldError *redaction.FieldError
	switch {
	case errors.As(err, &fieldError):
		return fieldError.MessageKey, "redaction_config." + fieldError.Field
	case errors.Is(err, errRedactionUnavailable):
		return "redaction.unavailable", "redaction_enabled"
	case errors.Is(err, errRedactionDisabled):
		return "redaction.disabled", "redaction_enabled"
	case errors.Is(err, redaction.ErrLimit):
		return "redaction.limit_exceeded", ""
	default:
		return "redaction.invalid_document", ""
	}
}

func rejectTokenRedaction(ctx *gin.Context, err error) {
	key, field := tokenRedactionErrorDetails(err)
	response := gin.H{"success": false, "message": err.Error(), "message_key": key}
	if field != "" {
		response["field"] = field
	}
	ctx.JSON(http.StatusOK, response)
}

func PreviewTokenRedaction(ctx *gin.Context) {
	ctx.Header("Cache-Control", "no-store")
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, redaction.MaxPolicyBytes+(16<<10))
	var draft struct {
		Config redaction.Config `json:"config"`
		Text   string           `json:"text"`
	}
	if err := ctx.ShouldBindJSON(&draft); err != nil || len(draft.Text) > 8192 {
		rejectTokenRedaction(ctx, redaction.ErrInvalidDocument)
		return
	}
	settings := redaction_setting.Get()
	if draft.Config.RestorePII && (!settings.Enabled || !settings.AllowResponseRestore || !redaction_setting.Readiness().ConfigureRestore || draft.Config.Strategy != "typed_placeholder") {
		rejectTokenRedaction(ctx, redactionRestorePolicyError())
		return
	}
	policy, err := redaction.Compile(draft.Config, settings.MaxCustomRulesPerKey, false)
	if err != nil {
		rejectTokenRedaction(ctx, err)
		return
	}
	session, err := policy.NewSession([]byte(common.GenerateHMAC("redaction-preview:" + strconv.Itoa(ctx.GetInt("id")))))
	if err != nil {
		rejectTokenRedaction(ctx, err)
		return
	}
	text, segments, err := session.Preview(draft.Text)
	if err != nil {
		rejectTokenRedaction(ctx, err)
		return
	}
	common.ApiSuccess(ctx, gin.H{"text": text, "counts": session.Counts(), "segments": segments})
}
