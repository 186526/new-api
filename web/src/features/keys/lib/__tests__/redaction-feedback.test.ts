import { expect, test, vi } from 'vitest'

import {
  attachRedactionError,
  needsRedactionRevision,
  redactionErrorMessage,
} from '../redaction-feedback'

test('prefers a recognized backend message key over legacy text', () => {
  const translate = vi.fn(() => 'Localized RE2 error')
  expect(redactionErrorMessage(translate, { message_key: 'redaction.invalid_pattern' }, 'Fallback')).toBe('Localized RE2 error')
  expect(translate).toHaveBeenCalledWith('invalid redaction configuration: custom_rules.pattern: use a valid nonempty RE2 pattern')
})

test('uses a stable translation key for an indexed RE2 error', () => {
  const translate = vi.fn(() => 'Localized RE2 error')
  const message = redactionErrorMessage(translate, {
    field: 'redaction_config.custom_rules.2.pattern',
    message: 'invalid redaction configuration: custom_rules.2.pattern: use a valid nonempty RE2 pattern',
  }, 'Fallback')

  expect(translate).toHaveBeenCalledWith('invalid redaction configuration: custom_rules.pattern: use a valid nonempty RE2 pattern')
  expect(message).toBe('Localized RE2 error')
})

test('uses a safe fallback for unknown indexed errors', () => {
  const translate = vi.fn()
  expect(redactionErrorMessage(translate, {
    field: 'redaction_config.allowlist.2',
    message: 'invalid redaction configuration: allowlist.2: unexpected detail',
  }, 'Fallback')).toBe('Fallback')
  expect(translate).not.toHaveBeenCalled()
})

test('maps a rejected RE2 pattern to the specific custom rule', () => {
  const setError = vi.fn()
  expect(attachRedactionError(setError, { field: 'redaction_config.custom_rules.2.pattern' }, 'Invalid RE2')).toBe(true)
  expect(setError).toHaveBeenCalledWith('redaction_config.custom_rules.2.pattern', { type: 'server', message: 'Invalid RE2' })
})

test('maps an invalid allowlist line to its shared textarea', () => {
  const setError = vi.fn()
  expect(attachRedactionError(setError, { field: 'redaction_config.allowlist.2' }, 'Invalid entry')).toBe(true)
  expect(setError).toHaveBeenCalledWith('redaction_config.allowlist', { type: 'server', message: 'Invalid entry' })
})

test('rejects unrelated and malformed field paths', () => {
  const setError = vi.fn()
  expect(attachRedactionError(setError, { field: 'redaction_config.custom_rules.0.__proto__' }, 'Invalid')).toBe(false)
  expect(setError).not.toHaveBeenCalled()
})

test('requires a policy revision even when the edit disables redaction', () => {
  expect(needsRedactionRevision({ redaction_enabled: false })).toBe(true)
  expect(needsRedactionRevision({})).toBe(false)
})
