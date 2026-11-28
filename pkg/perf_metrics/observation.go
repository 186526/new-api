package perfmetrics

import (
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func observationCounts(observed relaycommon.RequestObservation) model.PerfMetricObservation {
	var counts model.PerfMetricObservation
	if tool := observed.Tool; tool != nil {
		counts.ToolRequests = 1
		if tool.CacheHit {
			counts.ToolCacheHits = 1
		}
		switch tool.Contract {
		case "tools/search", "alpha/search", "moonshot/search":
			counts.ToolSearches = 1
		case "tools/fetch", "moonshot/fetch":
			counts.ToolFetches = 1
		}
		switch tool.Outcome {
		case "empty":
			counts.ToolEmpty = 1
		case "upstream_error", "unavailable":
			counts.ToolFailures = 1
		}
	}
	if image := observed.Image; image != nil {
		if work := image.Preprocessing; work != nil {
			counts.ImagePreprocessingRuns = 1
			counts.ImagesPreprocessed = int64(max(0, work.Processed))
		}
		if image.OutboundCompressed != nil {
			counts.ImageCountSamples = 1
			counts.ImagesCompressed = int64(max(0, *image.OutboundCompressed))
		}
		if image.OutboundSavedBytes != nil {
			counts.ImageSavingsSamples = 1
			counts.ImageBytesSaved = int64(max(0, *image.OutboundSavedBytes))
		} else if image.Reason == "compressed" {
			counts.ImageSavingsUnknown = 1
		}
	}
	if redaction := observed.Redaction; redaction != nil && redaction.Matches > 0 {
		counts.RedactedRequests = 1
		counts.RedactionMatches = int64(redaction.Matches)
		counts.RedactionCategories = int64(redaction.Categories)
	}
	if observed.AutoReason == "applied" {
		counts.AutomaticReasoning = 1
	}
	switch observed.AutoOutboundStatus {
	case "observed":
		counts.ReasoningObserved = 1
		if len(observed.AutoOutboundFields) > 0 {
			counts.ReasoningFieldsPresent = 1
		}
	case "":
	default:
		counts.ReasoningOutboundUnknown = 1
	}
	return counts
}

var observationFlushMu sync.RWMutex

func QueryRequestObservations(startTs, endTs int64, groups []string) (*model.PerfMetricObservation, error) {
	observationFlushMu.RLock()
	defer observationFlushMu.RUnlock()
	counts, err := model.GetPerfMetricObservations(startTs, endTs, groups)
	if err != nil {
		return nil, err
	}
	visitObservationBuckets(startTs, endTs, groups, func(bucket *atomicBucket) {
		counts.Add(bucket.observations())
	})
	refunds, err := model.GetQuotaRefundCounts(startTs, endTs, groups)
	counts.RefundRequested = 0
	counts.RefundCompleted = 0
	if err != nil {
		common.SysError("failed to read quota refund counts: " + err.Error())
	} else {
		counts.RefundRequested = refunds.Requested
		counts.RefundCompleted = refunds.Completed
		counts.RefundFailed = refunds.Failed
	}
	if counts == (model.PerfMetricObservation{}) {
		return nil, nil
	}
	return &counts, nil
}
