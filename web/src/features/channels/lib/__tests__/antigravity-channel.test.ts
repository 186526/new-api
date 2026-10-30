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
import { describe, expect, test } from 'vitest'

import {
  CHANNEL_TYPE_ANTIGRAVITY,
  CHANNEL_TYPE_OPTIONS,
  MODEL_FETCHABLE_TYPES,
} from '../../constants'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  channelFormSchema,
  type ChannelFormValues,
} from '../channel-form'

function antigravityForm(
  overrides: Partial<ChannelFormValues> = {}
): ChannelFormValues {
  return {
    ...CHANNEL_FORM_DEFAULT_VALUES,
    name: 'Antigravity',
    type: CHANNEL_TYPE_ANTIGRAVITY,
    key: JSON.stringify({ refresh_token: 'refresh-token' }),
    models: 'gemini-3-flash',
    ...overrides,
  }
}

function fieldIssue(form: ChannelFormValues, field: string) {
  const result = channelFormSchema.safeParse(form)
  if (result.success) return undefined
  return result.error.issues.find((issue) => issue.path.join('.') === field)
}

describe('Antigravity channel registration', () => {
  test('offers the type in the channel type picker', () => {
    expect(
      CHANNEL_TYPE_OPTIONS.some(
        (option) => option.value === CHANNEL_TYPE_ANTIGRAVITY
      )
    ).toBe(true)
  })

  test('lists the type as able to fetch models from upstream', () => {
    expect(MODEL_FETCHABLE_TYPES.has(CHANNEL_TYPE_ANTIGRAVITY)).toBe(true)
  })
})

describe('Antigravity credential validation', () => {
  test('accepts a JSON object with refresh_token', () => {
    expect(fieldIssue(antigravityForm(), 'key')).toBeUndefined()
  })

  test('rejects a JSON object without refresh_token', () => {
    const issue = fieldIssue(
      antigravityForm({ key: JSON.stringify({ access_token: 'token' }) }),
      'key'
    )

    expect(issue?.message).toBe(
      'Antigravity credential must be a JSON object with refresh_token'
    )
  })

  test('rejects a plain API key', () => {
    const issue = fieldIssue(
      antigravityForm({ key: 'sk-plain-api-key' }),
      'key'
    )

    expect(issue?.message).toBe(
      'Antigravity credential must be a JSON object with refresh_token'
    )
  })

  test('rejects batch creation', () => {
    const issue = fieldIssue(
      antigravityForm({ multi_key_mode: 'batch' }),
      'multi_key_mode'
    )

    expect(issue?.message).toBe(
      'Antigravity channels do not support batch creation'
    )
  })
})
