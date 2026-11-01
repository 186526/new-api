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
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { getCommandCodeUsage } from '../../api'
import { CommandCodeUsageDialog } from '../dialogs/commandcode-usage-dialog'

vi.mock('../../api', () => ({
  getCommandCodeUsage: vi.fn(),
}))

function renderDialog() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <CommandCodeUsageDialog
        open
        onOpenChange={() => {}}
        channelId={1}
        channelName='Command Code'
      />
    </QueryClientProvider>
  )
}

describe('CommandCodeUsageDialog', () => {
  beforeEach(() => {
    vi.mocked(getCommandCodeUsage).mockReset()
  })

  test('renders each account with its rolling windows', async () => {
    vi.mocked(getCommandCodeUsage).mockResolvedValue({
      success: true,
      data: {
        accounts: [
          {
            remaining_usd: 38.4,
            monthly_credits: 25,
            purchased_credits: 10,
            free_credits: 3.4,
            windows: [
              {
                name: '5h',
                used_usd: 1.2,
                cap_usd: 5,
                reset_at: 1793551234,
              },
              {
                name: 'weekly',
                used_usd: 8,
                cap_usd: 30,
                reset_at: 1793600000,
              },
            ],
          },
        ],
      },
    })

    renderDialog()

    expect(await screen.findByText('Account 1')).toBeTruthy()
    expect(
      screen.getByRole('dialog', { name: 'Command Code usage' })
    ).toBeInTheDocument()
    expect(screen.getByText('$38.4')).toBeTruthy()
    expect(
      screen.getByText('5h · $1.2 / $5', { collapseWhitespace: false })
    ).toBeTruthy()
    expect(screen.getByText('76%')).toBeTruthy()
    expect(
      screen.getByText('weekly · $8 / $30', { collapseWhitespace: false })
    ).toBeTruthy()
    expect(screen.getByText('73.3%')).toBeTruthy()
    expect(screen.getAllByText(/Reset at:/)).toHaveLength(2)
  })

  test('shows the upstream error and a retry action', async () => {
    vi.mocked(getCommandCodeUsage).mockRejectedValue(new Error('quota denied'))

    renderDialog()

    expect(await screen.findByText('quota denied')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeTruthy()
  })
})
