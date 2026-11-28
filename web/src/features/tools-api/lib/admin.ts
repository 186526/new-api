import { isAxiosError } from 'axios'

import { getChannels } from '@/features/channels/api'
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import type { ToolContract } from './group-policies'

type AdminResponse<T> = { success: boolean; data: T; message?: string }

export type PolicySnapshot = { value: string; version: string }
export type CacheSelection = {
  scope: 'forward' | 'redirect'
  query?: string
  url?: string
}
export type CacheStatus = {
  scope: CacheSelection['scope']
  backend: string
  coverage: string
  exact_invalidations: number
  generation?: string
  valid_entries?: number
  entries_truncated?: boolean
}
export type SimulationInput = {
  group: string
  contract: ToolContract
  channel_id?: number
  user_group?: string
  model?: string
}
type SimulationProvider = { provider: string; native: boolean; origin: string }

export type SimulationChannel = {
  group: string
  channel_id: number
  channel_name: string
  matched_model: string
  priority: number
  weight: number
  first_attempt: boolean
  providers: SimulationProvider[]
}

export type Simulation = {
  channels?: SimulationChannel[]
  preflight?: boolean
  authorization_verified?: boolean
  channel_is_sample?: boolean
  eligibility?: {
    group: string
    channel_id?: number
    provider?: string
    origin?: string
    reason: string
  }[]
  status: number
  group?: string
  channel_name?: string
  channel_id?: number
  channel_type?: number
  source?: string
  candidates: SimulationProvider[]
  steps: { group: string; reason: string }[]
  billing_model: string
  pricing_status: string
  billing_expression?: string
  legacy_price?: number
  legacy_model_ratio?: number
  tool_price_per_thousand?: number
  group_ratio?: number
  weighted_selection: boolean
}
export type ProviderSummary = {
  provider: string
  contracts: ToolContract[]
  native_contracts: ToolContract[]
  available_channels: number
  missing_mapping_channels: number
  global_credential_configured: boolean
  local_fallback: boolean
  contract_details?: {
    contract: ToolContract
    native: boolean
    available_channels: number
    missing_mapping_channels: number
    available_by_group?: Record<string, number>
    builtin_fallback_available: boolean
  }[]
}

const policiesPath = '/api/tools-api/group-policies'
const cachePath = '/api/option/websearch_cache'
export const policiesKey = ['tools-api', 'group-policies'] as const
export const summaryKey = ['tools-api', 'summary'] as const
export const cacheKey = ['tools-api', 'cache'] as const

export async function getPolicySnapshot() {
  const { data } = await api.get<AdminResponse<PolicySnapshot>>(policiesPath)
  return requireServerSuccess(data).data
}

export async function savePolicySnapshot(input: PolicySnapshot) {
  const { data } = await api.put<AdminResponse<PolicySnapshot>>(
    policiesPath,
    input
  )
  return requireServerSuccess(data).data
}

export function readPolicyConflict(error: unknown): PolicySnapshot | undefined {
  if (!isAxiosError<AdminResponse<PolicySnapshot>>(error)) return
  if (error.response?.status !== 409) return
  return error.response.data.data
}

export async function getProviderSummary() {
  const { data } = await api.get<
    AdminResponse<{ providers: ProviderSummary[] }>
  >('/api/tools-api/summary')
  const providers = requireServerSuccess(data).data?.providers
  if (!Array.isArray(providers)) throw new Error('Invalid provider directory')
  return providers
}

export async function getSimulatorChannels(signal: AbortSignal) {
  const channels = new Map<number, { id: number; type: number; name: string }>()
  for (let page = 1; !signal.aborted; page += 1) {
    const { data } = requireServerSuccess(
      await getChannels({ p: page, page_size: 100, status: 'enabled', id_sort: true })
    )
    if (!data?.items?.length) break
    for (const { id, type, name } of data.items) channels.set(id, { id, type, name })
    if (data.page_size <= 0 || page * data.page_size >= data.total) break
  }
  return [...channels.values()]
}

export async function simulateToolRoute(input: SimulationInput) {
  const { data } = await api.post<AdminResponse<Simulation>>(
    '/api/tools-api/simulate',
    input
  )
  return requireServerSuccess(data).data
}

export async function getCacheStatus() {
  const { data } =
    await api.get<AdminResponse<{ scopes: CacheStatus[] }>>(cachePath)
  return requireServerSuccess(data).data.scopes
}

export async function clearToolCache(input: CacheSelection) {
  const { data } = await api.delete<AdminResponse<CacheStatus>>(cachePath, {
    data: input,
  })
  return requireServerSuccess(data).data
}
