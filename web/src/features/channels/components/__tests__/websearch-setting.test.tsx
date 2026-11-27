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
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useForm } from 'react-hook-form'
import { describe, expect, test } from 'vitest'

import { Form } from '@/components/ui/form'

import { CHANNEL_TYPE_ANTIGRAVITY, CHANNEL_TYPE_OLLAMA } from '../../constants'
import {
  buildSettingJSON,
  CHANNEL_FORM_DEFAULT_VALUES,
  channelFormSchema,
  transformChannelToFormDefaults,
  type ChannelFormValues,
} from '../../lib/channel-form'
import { channelSchema } from '../../types'
import { WebSearchSetting } from '../websearch-setting'

const CHANNEL_TYPE_DEEPSEEK = 43

function createChannel(setting: string, type = CHANNEL_TYPE_ANTIGRAVITY) {
  return channelSchema.parse({
    id: 1,
    name: 'Test channel',
    key: '',
    type,
    status: 1,
    created_time: 0,
    test_time: 0,
    response_time: 0,
    balance_updated_time: 0,
    setting,
  })
}

function SettingsForm(props: {
  channelType: number
  defaultValues?: Partial<ChannelFormValues>
  onSave: (values: ChannelFormValues) => void
}) {
  const form = useForm<ChannelFormValues>({
    defaultValues: {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      ...props.defaultValues,
      type: props.channelType,
    },
  })

  return (
    <Form {...form}>
      <form onSubmit={form.handleSubmit(props.onSave)}>
        <WebSearchSetting channelType={props.channelType} />
        <button type='submit'>Save</button>
      </form>
    </Form>
  )
}

describe('Channel search and fetch settings visibility', () => {
  test('Antigravity and DeepSeek channels show the group', () => {
    const { unmount } = render(
      <SettingsForm
        channelType={CHANNEL_TYPE_ANTIGRAVITY}
        onSave={() => undefined}
      />
    )
    expect(
      screen.getByRole('combobox', { name: 'Search and fetch provider' })
    ).toBeVisible()
    unmount()

    render(
      <SettingsForm
        channelType={CHANNEL_TYPE_DEEPSEEK}
        onSave={() => undefined}
      />
    )
    expect(
      screen.getByRole('combobox', { name: 'Search and fetch provider' })
    ).toBeVisible()
  })

  test('other channel types hide the group', () => {
    render(
      <SettingsForm
        channelType={CHANNEL_TYPE_OLLAMA}
        onSave={() => undefined}
      />
    )

    expect(
      screen.queryByRole('combobox', { name: 'Search and fetch provider' })
    ).not.toBeInTheDocument()
  })
})

describe('Channel search and fetch settings values', () => {
  test('selecting a provider stores it in the setting JSON', async () => {
    const user = userEvent.setup()
    const saved: ChannelFormValues[] = []
    render(
      <SettingsForm
        channelType={CHANNEL_TYPE_ANTIGRAVITY}
        onSave={(values) => saved.push(values)}
      />
    )

    await user.click(
      screen.getByRole('combobox', { name: 'Search and fetch provider' })
    )
    await user.click(screen.getByRole('option', { name: 'DeepSeek' }))
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(saved[0].websearch_provider).toBe('deepseek')
    expect(JSON.parse(buildSettingJSON(saved[0])).websearch_provider).toBe(
      'deepseek'
    )
  })

  test('leaving the provider on the channel type omits the key', async () => {
    const user = userEvent.setup()
    const saved: ChannelFormValues[] = []
    render(
      <SettingsForm
        channelType={CHANNEL_TYPE_ANTIGRAVITY}
        onSave={(values) => saved.push(values)}
      />
    )

    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(saved[0].websearch_provider).toBeUndefined()
    expect(JSON.parse(buildSettingJSON(saved[0]))).not.toHaveProperty(
      'websearch_provider'
    )
  })

  test('reads the four settings back from a stored channel', () => {
    const channel = createChannel(
      JSON.stringify({
        websearch_provider: 'deepseek',
        antigravity_web_search_model: 'gemini-3.5-flash-lite',
        websearch_cache_ttl_seconds: 60,
        websearch_fetch_cache_ttl_seconds: -1,
      })
    )
    const values = transformChannelToFormDefaults(channel)

    expect(values.websearch_provider).toBe('deepseek')
    expect(values.antigravity_web_search_model).toBe('gemini-3.5-flash-lite')
    expect(values.websearch_cache_ttl_seconds).toBe(60)
    expect(values.websearch_fetch_cache_ttl_seconds).toBe(-1)
  })

  test('an unknown stored provider falls back to the channel type', () => {
    const values = transformChannelToFormDefaults(
      createChannel(JSON.stringify({ websearch_provider: 'google' }))
    )

    expect(values.websearch_provider).toBeUndefined()
  })

  test('a disabled cache (-1) survives the round trip to the setting JSON', () => {
    const setting = buildSettingJSON({
      ...CHANNEL_FORM_DEFAULT_VALUES,
      type: CHANNEL_TYPE_ANTIGRAVITY,
      websearch_cache_ttl_seconds: -1,
      websearch_fetch_cache_ttl_seconds: 0,
    })

    expect(JSON.parse(setting).websearch_cache_ttl_seconds).toBe(-1)
    expect(JSON.parse(setting).websearch_fetch_cache_ttl_seconds).toBe(0)
  })

  test('a channel type that cannot search stores none of the four settings', () => {
    const setting = buildSettingJSON({
      ...CHANNEL_FORM_DEFAULT_VALUES,
      type: CHANNEL_TYPE_OLLAMA,
      websearch_provider: 'deepseek',
      antigravity_web_search_model: 'gemini-3.5-flash-lite',
      websearch_cache_ttl_seconds: 60,
      websearch_fetch_cache_ttl_seconds: 60,
    })
    const stored = JSON.parse(setting)

    expect(stored).not.toHaveProperty('websearch_provider')
    expect(stored).not.toHaveProperty('antigravity_web_search_model')
    expect(stored).not.toHaveProperty('websearch_cache_ttl_seconds')
    expect(stored).not.toHaveProperty('websearch_fetch_cache_ttl_seconds')
  })

  test('rejects a cache TTL below -1 like the backend validator', () => {
    const form = {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      name: 'Antigravity',
      type: CHANNEL_TYPE_ANTIGRAVITY,
      key: JSON.stringify({ refresh_token: 'refresh-token' }),
      models: 'gemini-3-flash',
      websearch_cache_ttl_seconds: -2,
    }

    const result = channelFormSchema.safeParse(form)

    expect(result.success).toBe(false)
    expect(
      result.error?.issues.some(
        (issue) => issue.path.join('.') === 'websearch_cache_ttl_seconds'
      )
    ).toBe(true)
  })

  test('accepts -1 as the cache-disabled value', () => {
    const form = {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      name: 'Antigravity',
      type: CHANNEL_TYPE_ANTIGRAVITY,
      key: JSON.stringify({ refresh_token: 'refresh-token' }),
      models: 'gemini-3-flash',
      websearch_cache_ttl_seconds: -1,
      websearch_fetch_cache_ttl_seconds: -1,
    }

    expect(channelFormSchema.safeParse(form).success).toBe(true)
  })
})
