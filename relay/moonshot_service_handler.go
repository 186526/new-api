package relay

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/logger"
	appmetrics "github.com/QuantumNous/new-api/pkg/metrics"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/websearch"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/service/websearch/request"

	"github.com/gin-gonic/gin"
)

const (
	moonshotServiceOperationSearch = appmetrics.MoonshotServiceOperationSearch
	moonshotServiceOperationFetch  = appmetrics.MoonshotServiceOperationFetch

	moonshotServiceCacheHit  = appmetrics.MoonshotServiceCacheHit
	moonshotServiceCacheMiss = appmetrics.MoonshotServiceCacheMiss

	moonshotServiceOutcomeSuccess     = appmetrics.MoonshotServiceOutcomeSuccess
	moonshotServiceOutcomeEmpty       = appmetrics.MoonshotServiceOutcomeEmpty
	moonshotServiceOutcomeError       = appmetrics.MoonshotServiceOutcomeError
	moonshotServiceOutcomeUnsupported = appmetrics.MoonshotServiceOutcomeUnsupportedChannel
)

// MoonshotServiceHelper answers the Kimi Code search and fetch endpoints through
// the websearch pipeline. The client reads any non-200 as a failure, and a
// failed fetch then falls back to fetching locally inside the client, so a
// provider error is answered 200 with an empty body and logged here instead. A
// contract no candidate can serve is reported as a request error, the same way
// /v1/alpha/search and /v1/tools report one.
func MoonshotServiceHelper(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	info.InitChannelMeta(c)

	request_, ok := info.Request.(*dto.MoonshotServiceRequest)
	if !ok {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("invalid request type, expected *dto.MoonshotServiceRequest, got %T", info.Request),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}

	channel := websearchChannelContext(c)

	switch info.RelayMode {
	case relayconstant.RelayModeMoonshotSearch:
		return moonshotServiceSearch(c, info, channel, request_)
	case relayconstant.RelayModeMoonshotFetch:
		return moonshotServiceFetch(c, info, channel, request_)
	default:
		return types.NewErrorWithStatusCode(
			fmt.Errorf("unsupported moonshot service relay mode %d", info.RelayMode),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}
}

func moonshotServiceSearch(c *gin.Context, info *relaycommon.RelayInfo, channel abstract.ChannelContext, request_ *dto.MoonshotServiceRequest) *types.NewAPIError {
	query := request.Query{
		Contract: abstract.ContractMoonshotSearch,
		Text:     request_.TextQuery,
	}
	websearch.LogCandidates(c.Request.Context(), channel, query.Contract)

	answer, err := websearch.Search(c.Request.Context(), channel, query)
	if err != nil {
		if errors.Is(err, websearch.ErrUnsupportedChannel) {
			appmetrics.ObserveMoonshotServiceRequest(
				moonshotServiceOperationSearch,
				moonshotServiceOutcomeUnsupported,
				moonshotServiceCacheLabel(false),
			)
			return types.NewError(err, types.ErrorCodeInvalidRequest)
		}
		outcome := moonshotServiceOutcomeError
		if errors.Is(err, websearch.ErrEmptyResults) {
			// The provider answered, just with nothing in it. That is not a
			// failure to report, but it is still unbilled.
			outcome = moonshotServiceOutcomeEmpty
		} else {
			logger.LogWarn(c.Request.Context(), "moonshot service search failed for %q: %v", request_.TextQuery, err)
		}
		moonshotServiceRefund(c, info, moonshotServiceOperationSearch, outcome, false)
		writeMoonshotServiceEmptySearch(c)
		return nil
	}

	if emptySearchBody(answer.Body) {
		moonshotServiceRefund(c, info, moonshotServiceOperationSearch, moonshotServiceOutcomeEmpty, answer.Cached)
		writeMoonshotServiceEmptySearch(c)
		return nil
	}

	appmetrics.ObserveMoonshotServiceRequest(
		moonshotServiceOperationSearch,
		moonshotServiceOutcomeSuccess,
		moonshotServiceCacheLabel(answer.Cached),
	)
	writeMoonshotServiceBody(c, answer, "application/json")
	service.PostTextConsumeQuota(c, info, &dto.Usage{}, nil)
	return nil
}

func moonshotServiceFetch(c *gin.Context, info *relaycommon.RelayInfo, channel abstract.ChannelContext, request_ *dto.MoonshotServiceRequest) *types.NewAPIError {
	query := request.Query{
		Contract: abstract.ContractMoonshotFetch,
		URL:      request_.URL,
	}
	websearch.LogCandidates(c.Request.Context(), channel, query.Contract)

	answer, err := websearch.Fetch(c.Request.Context(), channel, query)
	if err != nil {
		if errors.Is(err, websearch.ErrUnsupportedChannel) {
			appmetrics.ObserveMoonshotServiceRequest(
				moonshotServiceOperationFetch,
				moonshotServiceOutcomeUnsupported,
				moonshotServiceCacheLabel(false),
			)
			return types.NewError(err, types.ErrorCodeInvalidRequest)
		}
		// The client swallows a failed fetch and fetches the page itself, so the
		// only record of the provider's failure is this line.
		outcome := moonshotServiceOutcomeError
		if errors.Is(err, websearch.ErrEmptyContent) {
			outcome = moonshotServiceOutcomeEmpty
		} else {
			logger.LogWarn(c.Request.Context(), "moonshot service fetch failed for %q: %v", request_.URL, err)
		}
		moonshotServiceRefund(c, info, moonshotServiceOperationFetch, outcome, false)
		writeMoonshotServicePage(c, "")
		return nil
	}

	if len(answer.Body) == 0 {
		moonshotServiceRefund(c, info, moonshotServiceOperationFetch, moonshotServiceOutcomeEmpty, answer.Cached)
		writeMoonshotServicePage(c, "")
		return nil
	}

	appmetrics.ObserveMoonshotServiceRequest(
		moonshotServiceOperationFetch,
		moonshotServiceOutcomeSuccess,
		moonshotServiceCacheLabel(answer.Cached),
	)
	writeMoonshotServiceBody(c, answer, "text/markdown; charset=utf-8")
	service.PostTextConsumeQuota(c, info, &dto.Usage{}, nil)
	return nil
}

// moonshotServiceRefund releases the reservation of a call that produced
// nothing. The upstream bills only a successful answer, so neither does this
// server.
func moonshotServiceRefund(c *gin.Context, info *relaycommon.RelayInfo, operation, outcome string, cached bool) {
	appmetrics.ObserveMoonshotServiceRequest(operation, outcome, moonshotServiceCacheLabel(cached))
	if info.Billing != nil {
		info.Billing.Refund(c)
	}
}

// moonshotServiceCacheLabel names where the answer came from, which is how a
// cache hit is told apart from an upstream call in the metric.
func moonshotServiceCacheLabel(cached bool) string {
	if cached {
		return moonshotServiceCacheHit
	}
	return moonshotServiceCacheMiss
}

// emptySearchBody reports an answer that carries no hit. The renderer writes an
// empty results array rather than an error, so the emptiness is read from the
// rendered document, which is the same document the client reads.
func emptySearchBody(body []byte) bool {
	return len(body) <= len(`{"search_results":[]}`)
}

func writeMoonshotServiceBody(c *gin.Context, answer websearch.Answer, fallbackContentType string) {
	contentType := answer.ContentType
	if contentType == "" {
		contentType = fallbackContentType
	}
	c.Header("Content-Type", contentType)
	c.Writer.WriteHeader(http.StatusOK)
	if _, err := c.Writer.Write(answer.Body); err != nil {
		logger.LogWarn(c.Request.Context(), "moonshot service: writing the answer failed: %v", err)
	}
}

// writeMoonshotServiceEmptySearch answers a search that produced no hit. The
// client reads the empty array as "nothing found" rather than as a failure.
func writeMoonshotServiceEmptySearch(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"search_results": []any{}})
}

func writeMoonshotServicePage(c *gin.Context, content string) {
	c.Header("Content-Type", "text/markdown; charset=utf-8")
	// The body is a page, not a format string: a percent sign inside it must
	// reach the client unchanged.
	c.String(http.StatusOK, "%s", content)
}
