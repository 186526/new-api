import { z } from 'zod'

import { fetchTokenKey, getApiKeys } from '@/features/keys/api'
import { API_KEY_STATUS } from '@/features/keys/constants'
import {
  TOOL_CONTRACTS,
  TOOL_ENDPOINTS,
} from '@/features/tools-api/lib/group-policies'
import { requireServerSuccess } from '@/lib/server-error-message'

const toolContract = z.custom<keyof typeof TOOL_ENDPOINTS>(
  (value) => typeof value === 'string' && Object.hasOwn(TOOL_ENDPOINTS, value)
)

export const toolsFormSchema = z
  .object({
    endpoint: toolContract,
    keyId: z.string().regex(/^[1-9]\d*$/),
    input: z.string().trim().min(1),
    model: z.string().trim(),
  })
  .superRefine((values, context) => {
    if (values.endpoint === 'alpha/search' && !values.model) {
      context.addIssue({ code: 'custom', path: ['model'] })
    }
    if (values.endpoint.endsWith('/fetch') && !isHttpUrl(values.input)) {
      context.addIssue({ code: 'custom', path: ['input'] })
    }
  })

export type ToolsFormValues = z.infer<typeof toolsFormSchema>

export const toolsPlaygroundSearchSchema = z
  .object({
    mode: z.literal('tools').optional().catch(undefined),
    endpoint: z
      .string()
      .optional()
      .catch(undefined)
      .transform((value) =>
        TOOL_CONTRACTS.find(
          (contract) => contract === value || TOOL_ENDPOINTS[contract] === value
        )
      ),
    group: z.string().trim().max(255).optional().catch(undefined),
    model: z.string().trim().min(1).max(255).optional().catch(undefined),
  })
  .transform((search) => ({
    ...search,
    model: search.endpoint === 'alpha/search' ? search.model : undefined,
  }))

function isHttpUrl(input: string): boolean {
  try {
    const url = new URL(input)
    return (
      (url.protocol === 'http:' || url.protocol === 'https:') &&
      !url.username &&
      !url.password
    )
  } catch {
    return false
  }
}

function buildPayload(values: ToolsFormValues) {
  switch (values.endpoint) {
    case 'tools/search':
      return { query: values.input }
    case 'moonshot/search':
      return { text_query: values.input }
    case 'tools/fetch':
    case 'moonshot/fetch':
      return { url: values.input }
    case 'alpha/search':
      return {
        model: values.model,
        commands: { search_query: [{ q: values.input }] },
      }
  }
}

type KeyOption = { id: number; name: string; group: string }

export async function listToolsKeys(signal: AbortSignal): Promise<KeyOption[]> {
  const options: KeyOption[] = []
  for (let page = 1; ; page += 1) {
    signal.throwIfAborted()
    const response = requireServerSuccess(
      await getApiKeys({ p: page, size: 100 })
    )
    const data = response.data
    if (!data?.items?.length) return options
    for (const token of data.items) {
      if (token.status === API_KEY_STATUS.ENABLED) {
        options.push({
          id: token.id,
          name: token.name,
          group: token.group ?? '',
        })
      }
    }
    if (data.page_size <= 0 || page * data.page_size >= data.total) {
      return options
    }
  }
}

export type ToolResponse = {
  status: number
  ok: boolean
  format: 'json' | 'markdown' | 'text'
  body: string
  empty: boolean
  invalidFormat: boolean
}

function formatResponse(
  response: Response,
  text: string,
  endpoint: ToolsFormValues['endpoint']
): ToolResponse {
  const result: ToolResponse = {
    status: response.status,
    ok: response.ok,
    format: 'text',
    body: text,
    empty: !text.trim(),
    invalidFormat: false,
  }
  const mediaType = response.headers
    .get('content-type')
    ?.split(';')[0]
    .trim()
    .toLowerCase()
  const markdown = endpoint === 'moonshot/fetch' && response.ok
  if (markdown && (mediaType === 'text/markdown' || !text.trim())) {
    return { ...result, format: 'markdown' }
  }
  if (mediaType === 'application/json' || mediaType?.endsWith('+json')) {
    try {
      const json: unknown = JSON.parse(text)
      const object =
        typeof json === 'object' && json !== null
          ? (json as Record<string, unknown>)
          : {}
      const results =
        endpoint === 'moonshot/search' ? object.search_results : object.results
      const emptyResult = Array.isArray(results) && results.length === 0
      const emptyContent =
        endpoint === 'tools/fetch' &&
        typeof object.content === 'string' &&
        !object.content.trim()
      return {
        ...result,
        format: 'json',
        body: JSON.stringify(json, null, 2),
        empty: response.ok && (emptyResult || emptyContent),
        invalidFormat: markdown,
      }
    } catch {
      return { ...result, invalidFormat: response.ok }
    }
  }
  return { ...result, invalidFormat: response.ok }
}

export async function runToolRequest(
  values: ToolsFormValues,
  signal: AbortSignal,
  channelId?: number
): Promise<ToolResponse> {
  signal.throwIfAborted()
  const keyResponse = requireServerSuccess(
    await fetchTokenKey(Number(values.keyId))
  )
  signal.throwIfAborted()
  const key = keyResponse.data?.key
  if (!key) throw new Error('Missing API key')

  const bearer = key.startsWith('sk-') ? key : `sk-${key}`
  const authorization = channelId === undefined ? bearer : `${bearer}-${channelId}`
  const response = await fetch(TOOL_ENDPOINTS[values.endpoint], {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${authorization}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(buildPayload(values)),
    credentials: 'omit',
    redirect: 'error',
    cache: 'no-store',
    signal,
  })
  const body = (await response.text())
    .replaceAll(authorization, '[REDACTED]')
    .replaceAll(bearer, '[REDACTED]')
    .replaceAll(key, '[REDACTED]')
  return formatResponse(response, body, values.endpoint)
}
