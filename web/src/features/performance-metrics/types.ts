/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
export type PerformanceSummary = {
  avg_latency_ms: number
  success_rate: number
  avg_tps: number
}

export type PerformanceSeriesPoint = {
  ts: number
  avg_ttft_ms: number
  avg_latency_ms: number
  success_rate: number
  avg_tps: number
}

export type PerformanceGroup = {
  group: string
  avg_ttft_ms: number
  avg_latency_ms: number
  success_rate: number
  avg_tps: number
  series: PerformanceSeriesPoint[]
}

export type PerformanceMetricsData = {
  success: boolean
  message?: string
  data: {
    summary?: PerformanceSummary | null
    series?: PerformanceSeriesPoint[]
    window_start?: number
    window_end?: number
    model_name: string
    series_schema?: string
    groups: PerformanceGroup[]
  }
}

export type SuccessRatePoint = { ts: number; success_rate: number }

export type PerfModelSummary = {
  model_name: string
  avg_latency_ms: number
  success_rate: number
  avg_tps: number
  recent_success_series?: SuccessRatePoint[]
  request_count?: number
}

export type RequestObservations = {
  tool_requests: number
  tool_cache_hits: number
  refund_requested: number
  refund_completed: number
  refund_failed?: number
  images_compressed: number
  image_bytes_saved: number
  images_preprocessed?: number
  image_preprocessing_runs?: number
  image_count_samples?: number
  image_savings_samples?: number
  image_savings_unknown?: number
  reasoning_fields_present?: number
  reasoning_outbound_unknown?: number
  reasoning_observed?: number
  redacted_requests: number
  redaction_matches: number
  automatic_reasoning: number
}

export type RequestObservationDistributionEntry = {
  kind: string
  name: string
  variant: string
  outcome: string
  count: number
  cache_hits?: number
}

export type PerfSummaryAllData = {
  success: boolean
  message?: string
  data: {
    summary?: PerformanceSummary | null
    window_start?: number
    window_end?: number
    request_observations?: RequestObservations
    request_observation_distribution?: RequestObservationDistributionEntry[]
    models: PerfModelSummary[]
    recent_success_series?: Record<string, SuccessRatePoint[]>
  }
}
