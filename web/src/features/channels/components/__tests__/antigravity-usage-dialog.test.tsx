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

import { getAntigravityUsage } from '../../api'
import { AntigravityUsageDialog } from '../dialogs/antigravity-usage-dialog'

vi.mock('../../api', () => ({
  getAntigravityUsage: vi.fn(),
}))

function renderDialog() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <AntigravityUsageDialog
        open
        onOpenChange={() => {}}
        channelId={1}
        channelName='Antigravity account'
      />
    </QueryClientProvider>
  )
}

describe('AntigravityUsageDialog', () => {
  beforeEach(() => {
    vi.mocked(getAntigravityUsage).mockReset()
  })

  test('renders the grouped windows with the remaining percentage', async () => {
    vi.mocked(getAntigravityUsage).mockResolvedValue({
      success: true,
      data: {
        groups: [
          {
            display_name: 'Gemini Models',
            description: 'Models within this group: Gemini Flash',
            buckets: [
              {
                bucket_id: 'gemini-5h',
                display_name: 'Five Hour Limit Remaining',
                window: '5h',
                remaining_fraction: 0.955,
                reset_time: '2026-10-30T13:52:32Z',
              },
            ],
          },
        ],
      },
    })

    renderDialog()

    expect(await screen.findByText('Gemini Models')).toBeTruthy()
    expect(screen.getByText('Five Hour Limit Remaining')).toBeTruthy()
    expect(screen.getByText('95.5%')).toBeTruthy()
    expect(screen.getByText(/Reset at:/)).toBeTruthy()
  })

  test('shows the upstream error and a retry action', async () => {
    vi.mocked(getAntigravityUsage).mockRejectedValue(new Error('quota denied'))

    renderDialog()

    expect(await screen.findByText('quota denied')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeTruthy()
  })
})
