import { expect, test } from 'vitest'

import {
  DEFAULT_REDACTION_CONFIG,
  getApiKeyFormDefaultValues,
  transformFormDataToPayload,
} from '../api-key-form'

const existing = () => ({
  ...getApiKeyFormDefaultValues(false),
  name: 'Protected key',
  redaction_enabled: true,
  redaction_config: DEFAULT_REDACTION_CONFIG,
})

test('editing an unrelated field omits the stored policy from the payload', () => {
  const payload = transformFormDataToPayload(existing(), {
    enabled: false,
    config: false,
  })

  expect(payload.name).toBe('Protected key')
  expect(payload).not.toHaveProperty('redaction_enabled')
  expect(payload).not.toHaveProperty('redaction_config')
})

test('disabling redaction leaves the existing policy unchanged', () => {
  const payload = transformFormDataToPayload(
    { ...existing(), redaction_enabled: false },
    { enabled: true, config: false }
  )

  expect(payload.redaction_enabled).toBe(false)
  expect(payload).not.toHaveProperty('redaction_config')
})
