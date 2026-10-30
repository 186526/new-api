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

import {
  buildSettingJSON,
  CHANNEL_FORM_DEFAULT_VALUES,
  transformChannelToFormDefaults,
  transformFormDataToCreatePayload,
  transformFormDataToUpdatePayload,
  type ChannelFormValues,
} from '../../lib/channel-form'
import { channelSchema, type Channel } from '../../types'
import { BalanceSourceSetting } from '../balance-source-setting'

function createChannel(setting: string, type = 1): Channel {
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
        <BalanceSourceSetting channelType={props.channelType} />
        <button type='submit'>Save</button>
      </form>
    </Form>
  )
}

describe('Channel balance source setting', () => {
  test('OpenAI channels show the selector and default to OpenAI Official', () => {
    render(<SettingsForm channelType={1} onSave={() => undefined} />)

    expect(
      screen.getByRole('combobox', { name: 'Balance Source' })
    ).toHaveTextContent('OpenAI Official')
  })

  test('non-OpenAI channels hide the selector and omit the setting', async () => {
    const user = userEvent.setup()
    const saved: ChannelFormValues[] = []
    render(
      <SettingsForm channelType={14} onSave={(values) => saved.push(values)} />
    )

    expect(
      screen.queryByRole('combobox', { name: 'Balance Source' })
    ).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(JSON.parse(buildSettingJSON(saved[0]))).not.toHaveProperty(
      'balance_source'
    )
  })

  test('selecting Command Code updates the form value and setting JSON', async () => {
    const user = userEvent.setup()
    const saved: ChannelFormValues[] = []
    render(
      <SettingsForm channelType={1} onSave={(values) => saved.push(values)} />
    )

    await user.click(screen.getByRole('combobox', { name: 'Balance Source' }))
    await user.click(screen.getByRole('option', { name: 'Command Code' }))
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(saved[0].balance_source).toBe('commandcode')
    expect(JSON.parse(buildSettingJSON(saved[0])).balance_source).toBe(
      'commandcode'
    )
  })

  test('editing an OpenAI channel reads Command Code back and keeps it on save', () => {
    const channel = createChannel(
      JSON.stringify({ balance_source: 'commandcode' })
    )
    const values = transformChannelToFormDefaults(channel)

    expect(values.balance_source).toBe('commandcode')

    const payloads = [
      transformFormDataToCreatePayload(values).channel,
      transformFormDataToUpdatePayload(values, channel.id),
    ]
    for (const payload of payloads) {
      expect(JSON.parse(String(payload.setting)).balance_source).toBe(
        'commandcode'
      )
    }
  })

  test('OpenAI channels store the empty default and read it back as OpenAI Official', () => {
    const setting = buildSettingJSON({
      ...CHANNEL_FORM_DEFAULT_VALUES,
      type: 1,
    })

    expect(JSON.parse(setting).balance_source).toBe('')
    expect(
      transformChannelToFormDefaults(createChannel(setting)).balance_source
    ).toBe('openai')
  })
})
