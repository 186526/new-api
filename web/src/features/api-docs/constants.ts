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

/** Identifier of one documented protocol group. */
export type ApiProtocolId =
  | 'openai-chat'
  | 'openai-responses'
  | 'anthropic'
  | 'gemini'
  | 'systemone'
  | 'moonshot-service'
  | 'deepseek-tools'

/** Languages a section renders one runnable example in. */
export type ApiSampleLanguage = 'curl' | 'python'

export type ApiEndpointDoc = {
  id: string
  method: 'GET' | 'POST'
  /** Path as this gateway serves it; `{model}` is replaced with a model name. */
  path: string
  /**
   * Endpoint type whose path from `/api/pricing.supported_endpoint` overrides
   * `path` when the backend reports one.
   */
  endpointType?: string
}

export type ApiProtocolDoc = {
  id: ApiProtocolId
  /** Model shown in the samples when `/api/pricing` lists none for this group. */
  fallbackModel: string
  /** Endpoint type used to pick a real model name from `/api/pricing`. */
  modelEndpointType: string
  endpoints: ApiEndpointDoc[]
}

export const ALPHA_SEARCH_ENDPOINT_TYPE = 'openai-alpha-search'

/**
 * The protocols in page order. Only structure lives here: paths, endpoint
 * types and fallback model names. Every user-visible string is a literal
 * `t('apiDocs.…')` call in `lib/copy.ts`.
 */
export const API_PROTOCOLS: ApiProtocolDoc[] = [
  {
    id: 'openai-chat',
    fallbackModel: 'gpt-6-luna',
    modelEndpointType: 'openai',
    endpoints: [
      {
        id: 'chat-completions',
        method: 'POST',
        path: '/v1/chat/completions',
        endpointType: 'openai',
      },
      {
        id: 'models',
        method: 'GET',
        path: '/v1/models',
      },
    ],
  },
  {
    id: 'openai-responses',
    fallbackModel: 'gpt-6-luna',
    modelEndpointType: 'openai-response',
    endpoints: [
      {
        id: 'responses',
        method: 'POST',
        path: '/v1/responses',
        endpointType: 'openai-response',
      },
      {
        id: 'responses-compact',
        method: 'POST',
        path: '/v1/responses/compact',
        endpointType: 'openai-response-compact',
      },
    ],
  },
  {
    id: 'anthropic',
    fallbackModel: 'claude-sonnet-5-5',
    modelEndpointType: 'anthropic',
    endpoints: [
      {
        id: 'messages',
        method: 'POST',
        path: '/v1/messages',
        endpointType: 'anthropic',
      },
    ],
  },
  {
    id: 'gemini',
    fallbackModel: 'gemini-3.8-flash',
    modelEndpointType: 'gemini',
    endpoints: [
      {
        id: 'generate-content',
        method: 'POST',
        path: '/v1beta/models/{model}:generateContent',
        endpointType: 'gemini',
      },
    ],
  },
  {
    id: 'systemone',
    fallbackModel: 'jev-latest',
    modelEndpointType: 'systemone',
    endpoints: [
      {
        id: 'systemone-models',
        method: 'GET',
        path: '/systemone/v1/models',
      },
      {
        id: 'systemone-decide',
        method: 'POST',
        path: '/systemone/v1/systemone',
        endpointType: 'systemone',
      },
    ],
  },
  {
    id: 'moonshot-service',
    fallbackModel: 'tools-search',
    modelEndpointType: 'moonshot-service-search',
    endpoints: [
      {
        id: 'moonshot-search',
        method: 'POST',
        path: '/moonshot.service/v1/search',
        endpointType: 'moonshot-service-search',
      },
      {
        id: 'moonshot-fetch',
        method: 'POST',
        path: '/moonshot.service/v1/fetch',
        endpointType: 'moonshot-service-fetch',
      },
    ],
  },
  {
    id: 'deepseek-tools',
    fallbackModel: 'tools-search',
    modelEndpointType: 'deepseek-tools-search',
    endpoints: [
      {
        id: 'tools-search',
        method: 'POST',
        path: '/v1/tools/search',
        endpointType: 'deepseek-tools-search',
      },
      {
        id: 'tools-fetch',
        method: 'POST',
        path: '/v1/tools/fetch',
        endpointType: 'deepseek-tools-fetch',
      },
      {
        id: 'alpha-search',
        method: 'POST',
        path: '/v1/alpha/search',
        endpointType: ALPHA_SEARCH_ENDPOINT_TYPE,
      },
    ],
  },
]

export const SAMPLE_LANGUAGES: ApiSampleLanguage[] = ['curl', 'python']

export const SAMPLE_LANGUAGE_LABELS: Record<ApiSampleLanguage, string> = {
  curl: 'cURL',
  python: 'Python',
}

/** Environment-variable name the samples read the API key from. */
export const API_KEY_ENV = 'NEW_API_KEY'
