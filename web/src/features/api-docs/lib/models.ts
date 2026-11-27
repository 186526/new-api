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
import type { PricingModel } from '@/features/pricing/types'

import type { ApiProtocolDoc } from '../constants'

/** `https://gateway.example.com` with no trailing slash. */
export function resolveBaseUrl(status: unknown): string {
  const record = status as Record<string, unknown> | null
  const nested = record?.data as Record<string, unknown> | undefined
  const candidate =
    record?.server_address ??
    record?.serverAddress ??
    nested?.server_address ??
    nested?.serverAddress

  if (typeof candidate === 'string' && candidate.trim()) {
    return candidate.trim().replace(/\/+$/, '')
  }
  if (typeof window !== 'undefined') {
    return window.location.origin
  }
  return 'https://api.example.com'
}

/**
 * One real model name per protocol, taken from `/api/pricing` by matching the
 * protocol's endpoint type. Falls back to the protocol's documented name when
 * the site lists no such model, so the samples always show something callable
 * on a default deployment.
 */
export function pickModelsByProtocol(
  protocols: ApiProtocolDoc[],
  models: PricingModel[] | undefined
): Record<string, string> {
  const result: Record<string, string> = {}

  for (const protocol of protocols) {
    const match = (models ?? []).find(
      (model) =>
        model.supported_endpoint_types?.includes(protocol.modelEndpointType) &&
        model.model_name
    )
    result[protocol.id] = match?.model_name ?? protocol.fallbackModel
  }

  return result
}

/**
 * The path the backend reports for an endpoint type, or the documented one.
 * Keeping this behind `/api/pricing` means a deployment that moves a path is
 * reflected in the docs without a rebuild.
 */
export function resolveEndpointPath(
  documentedPath: string,
  endpointType: string | undefined,
  supportedEndpoint: Record<string, { path?: string }> | undefined
): string {
  if (!endpointType) return documentedPath
  const reported = supportedEndpoint?.[endpointType]?.path
  return reported && reported.trim() ? reported : documentedPath
}

export function withModelInPath(path: string, model: string): string {
  return path.replace('{model}', model)
}
