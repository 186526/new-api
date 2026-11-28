package middleware

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/service/redaction"
	"github.com/gin-gonic/gin"
)

const responseRedactionSessionKey = "request_redaction_response_session"

func supportsResponseRestore(ctx *gin.Context, format redaction.Format, handshake bool) bool {
	if handshake || ctx.GetBool(responsesWSFrameKey) {
		return true
	}
	switch format {
	case redaction.Chat, redaction.Claude:
		return true
	case redaction.Responses:
		return ctx.FullPath() == "/v1/responses"
	case redaction.Gemini:
		return !strings.HasSuffix(ctx.Param("path"), ":batchGenerateContent")
	}
	return false
}

func requestRedactionSession(ctx *gin.Context) *redaction.Session {
	value, _ := ctx.Get(responseRedactionSessionKey)
	session, _ := value.(*redaction.Session)
	return session
}

func CheckProtectedControlIdentifiers(ctx *gin.Context, values ...string) error {
	session := requestRedactionSession(ctx)
	if session == nil {
		return errors.New("request protection policy is unavailable")
	}
	return session.CheckControlIdentifiers(values...)
}

func NewResponseRedaction(ctx *gin.Context, format redaction.Format) *redaction.ResponseRestorer {
	session := requestRedactionSession(ctx)
	if session == nil {
		return nil
	}
	return session.NewResponseRestorer(format)
}
