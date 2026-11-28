import type { FieldPath, UseFormSetError } from 'react-hook-form'

import type { ApiResponse } from '../types'
import type { ApiKeyFormValues } from './api-key-form'

const groupFields = new Set([
  'strategy',
  'scopes',
  'builtin_rules',
  'custom_rules',
  'allowlist',
  'restore_pii_in_response',
])

const legacyIndexedMessages = new Set([
  'invalid redaction configuration: custom_rules.id: invalid or repeated rule ID',
  'invalid redaction configuration: custom_rules.label: use 2–24 uppercase letters, digits or underscores',
  'invalid redaction configuration: custom_rules.replacement: replacement is too long',
  'invalid redaction configuration: custom_rules.pattern: use a valid nonempty RE2 pattern',
  'invalid redaction configuration: allowlist: entry must contain 1–128 bytes',
  'invalid redaction configuration: allowlist: use a valid nonempty RE2 pattern',
])

const legacyMessages = new Set([
  'invalid redaction document',
  'redaction processing limit exceeded',
  'request redaction is unavailable',
  'request redaction is disabled by the administrator',
  'invalid redaction configuration: strategy: unsupported strategy',
  'invalid redaction configuration: scopes: select at least one scope',
  'invalid redaction configuration: builtin_rules: select at least one rule',
  'invalid redaction configuration: builtin_rules: unknown or repeated rule',
  'invalid redaction configuration: custom_rules: too many rules',
  'invalid redaction configuration: allowlist: too many entries',
  'invalid redaction configuration: restore_pii_in_response: response restoration is disabled for this policy',
])

const stableMessages: Record<string, string> = {
  'redaction.unavailable': 'request redaction is unavailable',
  'redaction.disabled': 'request redaction is disabled by the administrator',
  'redaction.limit_exceeded': 'redaction processing limit exceeded',
  'redaction.invalid_document': 'invalid redaction document',
  'redaction.unsupported_strategy': 'invalid redaction configuration: strategy: unsupported strategy',
  'redaction.scope_required': 'invalid redaction configuration: scopes: select at least one scope',
  'redaction.rule_required': 'invalid redaction configuration: builtin_rules: select at least one rule',
  'redaction.too_many_rules': 'invalid redaction configuration: custom_rules: too many rules',
  'redaction.too_many_allowlist_entries': 'invalid redaction configuration: allowlist: too many entries',
  'redaction.invalid_builtin_rule': 'invalid redaction configuration: builtin_rules: unknown or repeated rule',
  'redaction.invalid_rule_id': 'invalid redaction configuration: custom_rules.id: invalid or repeated rule ID',
  'redaction.invalid_rule_label': 'invalid redaction configuration: custom_rules.label: use 2–24 uppercase letters, digits or underscores',
  'redaction.replacement_too_long': 'invalid redaction configuration: custom_rules.replacement: replacement is too long',
  'redaction.invalid_pattern': 'invalid redaction configuration: custom_rules.pattern: use a valid nonempty RE2 pattern',
  'redaction.invalid_allowlist_entry': 'invalid redaction configuration: allowlist: entry must contain 1–128 bytes',
  'redaction.restore_unavailable': 'invalid redaction configuration: restore_pii_in_response: response restoration is disabled for this policy',
}

export function redactionErrorMessage(
  t: (key: string) => string,
  response: Pick<ApiResponse, 'field' | 'message' | 'message_key'>,
  fallback: string
): string {
  if (response.message_key && Object.hasOwn(stableMessages, response.message_key)) {
    return t(stableMessages[response.message_key])
  }
  if (!response.message) return fallback
  const match = /^invalid redaction configuration: ([a-z_]+(?:\.\d+(?:\.[a-z_]+)?)?): ([^.]+)$/.exec(response.message)
  if (match && response.field === `redaction_config.${match[1]}`) {
    const normalizedField = match[1].replace(/\.\d+(?=\.|$)/, '')
    const key = `invalid redaction configuration: ${normalizedField}: ${match[2]}`
    if (legacyIndexedMessages.has(key) || legacyMessages.has(key)) return t(key)
  }
  return legacyMessages.has(response.message) ? t(response.message) : fallback
}

export function attachRedactionError(
  setError: UseFormSetError<ApiKeyFormValues>,
  response: Pick<ApiResponse, 'field'>,
  message: string
): boolean {
  const prefix = 'redaction_config.'
  if (!response.field?.startsWith(prefix)) return false

  let field = response.field.slice(prefix.length)
  if (/^allowlist\.\d+$/.test(field)) field = 'allowlist'
  if (!groupFields.has(field) && !/^custom_rules\.\d+\.(id|label|pattern|replacement)$/.test(field)) {
    return false
  }

  setError(`${prefix}${field}` as FieldPath<ApiKeyFormValues>, {
    type: 'server',
    message,
  })
  return true
}

export function needsRedactionRevision(patch: {
  redaction_enabled?: boolean
  redaction_config?: unknown
}): boolean {
  return patch.redaction_enabled !== undefined || patch.redaction_config !== undefined
}
