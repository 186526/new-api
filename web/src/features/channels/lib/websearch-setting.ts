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
import { z } from 'zod'

import {
  CHANNEL_TYPE_ADVANCED_CUSTOM,
  CHANNEL_TYPE_CODEX,
  TOOLS_FETCH_MODEL,
  TOOLS_SEARCH_MODEL,
} from '../constants'
import {
  formatModelsArray,
  parseModelsString,
} from './model-mapping-validation'

const operationSchema = z.object({
  supported: z.boolean(),
  native_contracts: z.array(z.string()),
  mapping_required_contracts: z.array(z.string()),
  fixed_model: z.string().optional(),
})
const providerSchema = z.object({
  provider: z.string(),
  search: operationSchema,
  fetch: operationSchema,
})
export const channelToolCatalogSchema = z.object({
  channel_types: z.record(z.string(), providerSchema),
  fallbacks: z.array(providerSchema),
})
export type ChannelToolCatalog = z.infer<typeof channelToolCatalogSchema>
export type ToolModel = typeof TOOLS_SEARCH_MODEL | typeof TOOLS_FETCH_MODEL
export const CODEX_SEARCH_MAPPING_REQUIRED =
  'Enter a valid upstream model for Codex search.'

export function isToolModel(model: string): model is ToolModel {
  return model === TOOLS_SEARCH_MODEL || model === TOOLS_FETCH_MODEL
}

export function preserveToolModels(current: string, models: string[]): string {
  return formatModelsArray([
    ...models.filter((model) => !isToolModel(model.trim())),
    ...parseModelsString(current).filter(isToolModel),
  ])
}

export function getChannelToolAvailability(
  channelType: number,
  catalog?: ChannelToolCatalog,
  preference?: string
) {
  const provider = catalog?.channel_types[channelType]
  const candidates = catalog ? [...Object.values(catalog.channel_types), ...catalog.fallbacks] : []
  const bound = preference === 'antigravity' || preference === 'deepseek' ||
    (preference === 'exa' && channelType !== CHANNEL_TYPE_ADVANCED_CUSTOM)
    ? candidates.find((candidate) => candidate.provider === preference)
    : undefined
  const search = bound?.search.supported ? bound.search : provider?.search
  const fetch = bound?.fetch.supported ? bound.fetch : provider?.fetch
  const canConfigure = Boolean(
    channelType === CHANNEL_TYPE_ADVANCED_CUSTOM || search?.supported || fetch?.supported
  )
  const localFetch = catalog?.fallbacks.some(
    (fallback) => fallback.provider === 'local' && fallback.fetch.supported
  )

  return {
    provider,
    canConfigure,
    canSearch: Boolean(search?.supported),
    canFetch: Boolean(fetch?.supported || (canConfigure && localFetch)),
    localFetch,
    requiresSearchMapping: catalog
      ? Boolean(search?.mapping_required_contracts.includes('tools/search'))
      : channelType === CHANNEL_TYPE_CODEX,
    requiresFetchMapping: Boolean(
      fetch?.mapping_required_contracts.includes('tools/fetch')
    ),
  }
}

function readMapping(value: string): Record<string, string> | undefined {
  try {
    const mapping: unknown = JSON.parse(value.trim() || '{}')
    if (!mapping || typeof mapping !== 'object' || Array.isArray(mapping)) {
      return
    }
    if (Object.values(mapping).some((target) => typeof target !== 'string')) {
      return
    }
    return mapping as Record<string, string>
  } catch {
    return undefined
  }
}

export function toolModelMapping(value: string, model: ToolModel): string | undefined {
  const mapping = readMapping(value)
  return mapping && (mapping[model] ?? '')
}

export function withToolModelMapping(value: string, model: ToolModel, target: string): string | undefined {
  const mapping = readMapping(value)
  if (!mapping) return undefined
  if (target) {
    mapping[model] = target
  } else {
    delete mapping[model]
  }
  return JSON.stringify(mapping, null, 2)
}

export function validToolModelMapping(value: string, toolModel: ToolModel): boolean {
  const mapping = readMapping(value)
  if (!mapping) return false
  const visited = new Set<string>()
  let model: string = toolModel
  while (!visited.has(model)) {
    visited.add(model)
    const target = Object.hasOwn(mapping, model) ? mapping[model].trim() : ''
    if (!target || target === model) {
      return !isToolModel(model) && !/[/?#\\ \r\n\t]/.test(model)
    }
    model = target
  }
  return false
}
