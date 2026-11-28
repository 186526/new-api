package model

import "gorm.io/gorm"

type PerfMetricObservation struct {
	ImagesPreprocessed       int64 `json:"images_preprocessed" gorm:"default:0"`
	ImagePreprocessingRuns   int64 `json:"image_preprocessing_runs" gorm:"default:0"`
	ImageCountSamples        int64 `json:"image_count_samples" gorm:"default:0"`
	ImageSavingsSamples      int64 `json:"image_savings_samples" gorm:"default:0"`
	ImageSavingsUnknown      int64 `json:"image_savings_unknown" gorm:"default:0"`
	ReasoningFieldsPresent   int64 `json:"reasoning_fields_present" gorm:"default:0"`
	ReasoningOutboundUnknown int64 `json:"reasoning_outbound_unknown" gorm:"default:0"`
	ToolRequests             int64 `json:"tool_requests" gorm:"default:0"`
	ToolCacheHits            int64 `json:"tool_cache_hits" gorm:"default:0"`
	RefundRequested          int64 `json:"refund_requested" gorm:"default:0"`
	RefundCompleted          int64 `json:"refund_completed" gorm:"default:0"`
	RefundFailed             int64 `json:"refund_failed" gorm:"-"`
	ImagesCompressed         int64 `json:"images_compressed" gorm:"default:0"`
	ImageBytesSaved          int64 `json:"image_bytes_saved" gorm:"default:0"`
	RedactedRequests         int64 `json:"redacted_requests" gorm:"default:0"`
	RedactionMatches         int64 `json:"redaction_matches" gorm:"default:0"`
	AutomaticReasoning       int64 `json:"automatic_reasoning" gorm:"default:0"`
	ToolSearches             int64 `json:"tool_searches" gorm:"default:0"`
	ToolFetches              int64 `json:"tool_fetches" gorm:"default:0"`
	ToolEmpty                int64 `json:"tool_empty" gorm:"default:0"`
	ToolFailures             int64 `json:"tool_failures" gorm:"default:0"`
	RedactionCategories      int64 `json:"redaction_categories" gorm:"default:0"`
	ReasoningObserved        int64 `json:"reasoning_observed" gorm:"default:0"`
}

func (m *PerfMetricObservation) Add(other PerfMetricObservation) {
	m.ImagesPreprocessed += other.ImagesPreprocessed
	m.ImagePreprocessingRuns += other.ImagePreprocessingRuns
	m.ImageCountSamples += other.ImageCountSamples
	m.ImageSavingsSamples += other.ImageSavingsSamples
	m.ImageSavingsUnknown += other.ImageSavingsUnknown
	m.ReasoningFieldsPresent += other.ReasoningFieldsPresent
	m.ReasoningOutboundUnknown += other.ReasoningOutboundUnknown
	m.ToolRequests += other.ToolRequests
	m.ToolCacheHits += other.ToolCacheHits
	m.RefundRequested += other.RefundRequested
	m.RefundCompleted += other.RefundCompleted
	m.ImagesCompressed += other.ImagesCompressed
	m.ImageBytesSaved += other.ImageBytesSaved
	m.RedactedRequests += other.RedactedRequests
	m.RedactionMatches += other.RedactionMatches
	m.AutomaticReasoning += other.AutomaticReasoning
	m.ToolSearches += other.ToolSearches
	m.ToolFetches += other.ToolFetches
	m.ToolEmpty += other.ToolEmpty
	m.ToolFailures += other.ToolFailures
	m.RedactionCategories += other.RedactionCategories
	m.ReasoningObserved += other.ReasoningObserved
}

const perfMetricObservationSums = ", SUM(tool_requests) AS tool_requests, SUM(tool_cache_hits) AS tool_cache_hits, SUM(refund_requested) AS refund_requested, SUM(refund_completed) AS refund_completed, SUM(images_compressed) AS images_compressed, SUM(image_bytes_saved) AS image_bytes_saved, SUM(redacted_requests) AS redacted_requests, SUM(redaction_matches) AS redaction_matches, SUM(automatic_reasoning) AS automatic_reasoning" +
	", SUM(tool_searches) AS tool_searches, SUM(tool_fetches) AS tool_fetches, SUM(tool_empty) AS tool_empty, SUM(tool_failures) AS tool_failures, SUM(redaction_categories) AS redaction_categories, SUM(reasoning_observed) AS reasoning_observed" +
	", SUM(images_preprocessed) AS images_preprocessed, SUM(image_preprocessing_runs) AS image_preprocessing_runs, SUM(image_count_samples) AS image_count_samples, SUM(image_savings_samples) AS image_savings_samples, SUM(image_savings_unknown) AS image_savings_unknown" +
	", SUM(reasoning_fields_present) AS reasoning_fields_present, SUM(reasoning_outbound_unknown) AS reasoning_outbound_unknown"

func GetPerfMetricObservations(startTs, endTs int64, groups []string) (PerfMetricObservation, error) {
	var result PerfMetricObservation
	if groups != nil && len(groups) == 0 {
		return result, nil
	}
	query := DB.Model(&PerfMetric{}).Select("COUNT(*) AS bucket_count"+perfMetricObservationSums).
		Where("bucket_ts >= ? AND bucket_ts <= ?", startTs, endTs)
	if groups != nil {
		query = query.Where(commonGroupCol+" IN ?", groups)
	}
	err := query.Having("COUNT(*) > 0").Scan(&result).Error
	return result, err
}

func (m PerfMetricObservation) addAssignments(assignments map[string]any) {
	assignments["images_preprocessed"] = gorm.Expr("perf_metrics.images_preprocessed + ?", m.ImagesPreprocessed)
	assignments["image_preprocessing_runs"] = gorm.Expr("perf_metrics.image_preprocessing_runs + ?", m.ImagePreprocessingRuns)
	assignments["image_count_samples"] = gorm.Expr("perf_metrics.image_count_samples + ?", m.ImageCountSamples)
	assignments["image_savings_samples"] = gorm.Expr("perf_metrics.image_savings_samples + ?", m.ImageSavingsSamples)
	assignments["image_savings_unknown"] = gorm.Expr("perf_metrics.image_savings_unknown + ?", m.ImageSavingsUnknown)
	assignments["reasoning_fields_present"] = gorm.Expr("perf_metrics.reasoning_fields_present + ?", m.ReasoningFieldsPresent)
	assignments["reasoning_outbound_unknown"] = gorm.Expr("perf_metrics.reasoning_outbound_unknown + ?", m.ReasoningOutboundUnknown)
	assignments["tool_requests"] = gorm.Expr("perf_metrics.tool_requests + ?", m.ToolRequests)
	assignments["tool_cache_hits"] = gorm.Expr("perf_metrics.tool_cache_hits + ?", m.ToolCacheHits)
	assignments["refund_requested"] = gorm.Expr("perf_metrics.refund_requested + ?", m.RefundRequested)
	assignments["refund_completed"] = gorm.Expr("perf_metrics.refund_completed + ?", m.RefundCompleted)
	assignments["images_compressed"] = gorm.Expr("perf_metrics.images_compressed + ?", m.ImagesCompressed)
	assignments["image_bytes_saved"] = gorm.Expr("perf_metrics.image_bytes_saved + ?", m.ImageBytesSaved)
	assignments["redacted_requests"] = gorm.Expr("perf_metrics.redacted_requests + ?", m.RedactedRequests)
	assignments["redaction_matches"] = gorm.Expr("perf_metrics.redaction_matches + ?", m.RedactionMatches)
	assignments["automatic_reasoning"] = gorm.Expr("perf_metrics.automatic_reasoning + ?", m.AutomaticReasoning)
	assignments["tool_searches"] = gorm.Expr("perf_metrics.tool_searches + ?", m.ToolSearches)
	assignments["tool_fetches"] = gorm.Expr("perf_metrics.tool_fetches + ?", m.ToolFetches)
	assignments["tool_empty"] = gorm.Expr("perf_metrics.tool_empty + ?", m.ToolEmpty)
	assignments["tool_failures"] = gorm.Expr("perf_metrics.tool_failures + ?", m.ToolFailures)
	assignments["redaction_categories"] = gorm.Expr("perf_metrics.redaction_categories + ?", m.RedactionCategories)
	assignments["reasoning_observed"] = gorm.Expr("perf_metrics.reasoning_observed + ?", m.ReasoningObserved)
}
