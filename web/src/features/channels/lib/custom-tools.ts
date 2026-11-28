import { t } from 'i18next'
import { z } from 'zod'

import {
  getServerErrorMessageKey,
  getServerErrorSources,
  getServerErrorStatus,
  isServerErrorCancelled,
  safeServerErrorMessage,
} from '@/lib/server-error-message'

export type CustomToolOperation = 'search' | 'fetch'

export const customToolStatusSchema = z.object({
  configured: z.boolean(),
  verified: z.boolean(),
  protocol: z.string().optional(),
  path: z.string().optional(),
})
export const customToolsSchema = z.object({
  search: customToolStatusSchema,
  fetch: customToolStatusSchema,
})

export type CustomToolStatus = z.infer<typeof customToolStatusSchema>
export type CustomTools = z.infer<typeof customToolsSchema>
export type CustomToolChange =
  | { operation: CustomToolOperation; action: 'verify' | 'remove' }
  | {
      operation: CustomToolOperation
      action: 'save'
      path: string
      apiKey: string
    }

const customToolPathSchema = z
  .string()
  .trim()
  .min(2)
  .max(512)
  .regex(/^(?:\/[\w.~-]+)+$/)
  .refine(
    (value) => !value.split('/').some((part) => part === '.' || part === '..')
  )

export function customToolFormSchema(configured: boolean) {
  return z.object({
    path: customToolPathSchema,
    apiKey: z
      .string()
      .max(4096)
      .regex(/^[!-~]*$/)
      .refine((value) => configured || value.length > 0),
  })
}

export type CustomToolFormValues = z.infer<
  ReturnType<typeof customToolFormSchema>
>

export function customToolDefaultPath(operation: CustomToolOperation): string {
  return operation === 'search' ? '/search' : '/contents'
}

export class CustomToolRequestError extends Error {
  readonly [safeServerErrorMessage] = true
  readonly conflict: boolean

  constructor(error: unknown, fallback: string) {
    const status = getServerErrorStatus(error)
    const codes = getServerErrorSources(error).flatMap((source) => [
      source.message_key,
      source.code,
      source.message,
    ])
    const sharedKey = getServerErrorMessageKey(error)
    super(
      typeof sharedKey === 'string'
        ? sharedKey
        : customToolErrorMessage(codes, status, fallback)
    )
    this.conflict =
      status === 409 || codes.includes('custom_tool_configuration_changed')
    if (isServerErrorCancelled(error)) this.name = 'AbortError'
  }
}

function customToolErrorMessage(
  codes: unknown[],
  status: number | undefined,
  fallback: string
): string {
  if (codes.includes('invalid_custom_tool_endpoint')) {
    return t('Enter a path beginning with /.')
  }
  if (
    codes.includes('invalid_custom_tool_credential') ||
    codes.includes('missing_custom_tool_credential')
  ) {
    return t('Enter a valid API key.')
  }
  if (codes.includes('custom_tool_not_configured')) return t('Not configured')
  if (codes.includes('custom_tool_invalid_response')) {
    return t('Invalid response')
  }
  if (status === 429 || codes.includes('custom_tool_verification_busy')) {
    return t('Too many requests')
  }
  if (codes.includes('custom_tool_read_failed')) {
    return t('Failed to load tool configuration.')
  }
  if (status === 401) return t('Session expired!')
  if (status === 403) {
    return t('You do not have permission to perform this action.')
  }
  if (status === 400 || codes.includes('invalid_custom_tool_configuration')) {
    return t('Check the tool path and API key.')
  }
  return fallback
}
