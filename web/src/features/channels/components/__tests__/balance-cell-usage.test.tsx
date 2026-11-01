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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { CHANNEL_TYPE_MISTRAL } from '../../constants'
import { channelSchema, type Channel } from '../../types'
import { BalanceCell } from '../channels-columns'
import { ChannelsProvider } from '../channels-provider'

function channel(overrides: Partial<Channel>): Channel {
  return channelSchema.parse({
    id: 7,
    type: 1,
    key: 'test-key',
    name: 'Test channel',
    status: 1,
    created_time: 1,
    test_time: 0,
    response_time: 0,
    balance_updated_time: 0,
    ...overrides,
  })
}

function renderCell(item: Channel) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <ChannelsProvider>
        <BalanceCell channel={item} />
      </ChannelsProvider>
    </QueryClientProvider>
  )
}

test('opens the Mistral usage dialog from the balance cell of a Mistral channel', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValue({ data: { success: true, data: { accounts: [] } } })
  const user = userEvent.setup()
  renderCell(channel({ type: CHANNEL_TYPE_MISTRAL, name: 'Mistral Vibe' }))

  await user.click(screen.getByText('Usage'))

  expect(
    await screen.findByRole('dialog', { name: 'Mistral usage' })
  ).toBeInTheDocument()
  expect(get).toHaveBeenCalledWith(
    '/api/channel/7/mistral/usage',
    expect.objectContaining({ disableDuplicate: true })
  )
  get.mockRestore()
})

test('opens the Command Code usage dialog from an OpenAI channel with the commandcode balance source', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValue({ data: { success: true, data: { accounts: [] } } })
  const user = userEvent.setup()
  renderCell(
    channel({
      type: 1,
      name: 'Command Code',
      setting: JSON.stringify({ balance_source: 'commandcode' }),
    })
  )

  await user.click(screen.getByText('Usage'))

  expect(
    await screen.findByRole('dialog', { name: 'Command Code usage' })
  ).toBeInTheDocument()
  expect(get).toHaveBeenCalledWith(
    '/api/channel/7/commandcode/usage',
    expect.objectContaining({ disableDuplicate: true })
  )
  get.mockRestore()
})

test('keeps the balance update action for an OpenAI channel without the commandcode balance source', async () => {
  const user = userEvent.setup()
  renderCell(channel({ type: 1, name: 'OpenAI' }))

  expect(screen.queryByText('Usage')).toBeNull()

  await user.hover(screen.getAllByText(/^\$/)[1])
  expect(await screen.findByText('Click to update balance')).toBeInTheDocument()
})
