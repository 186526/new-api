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

import { getMistralUsage } from '../../api'
import { MistralUsageDialog } from '../dialogs/mistral-usage-dialog'

vi.mock('../../api', () => ({
  getMistralUsage: vi.fn(),
}))

function renderDialog() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <MistralUsageDialog
        open
        onOpenChange={() => {}}
        channelId={1}
        channelName='Mistral Vibe'
      />
    </QueryClientProvider>
  )
}

describe('MistralUsageDialog', () => {
  beforeEach(() => {
    vi.mocked(getMistralUsage).mockReset()
  })

  test('renders the remaining ratio, amount and reset time', async () => {
    vi.mocked(getMistralUsage).mockResolvedValue({
      success: true,
      data: {
        channel_name: 'Mistral Vibe',
        usage_percentage: 0.1234,
        remaining_ratio: 0.998766,
        monthly_limit_usd: 150,
        remaining_usd: 149.81,
        reset_at: 1796083200,
        payg_enabled: false,
        fetched_at: 1793550000,
      },
    })

    renderDialog()

    expect(await screen.findByText('$149.81 / $150')).toBeTruthy()
    expect(
      screen.getByRole('dialog', { name: 'Mistral usage' })
    ).toBeInTheDocument()
    expect(screen.getByText('99.9%')).toBeTruthy()
    expect(screen.getByText(/Reset at:/)).toBeTruthy()
    expect(screen.getByText('Disabled')).toBeTruthy()
  })

  test('hides the amount row when the remaining amount is zero', async () => {
    vi.mocked(getMistralUsage).mockResolvedValue({
      success: true,
      data: {
        usage_percentage: 1,
        remaining_ratio: 0,
        monthly_limit_usd: 150,
        remaining_usd: 0,
        reset_at: 0,
        payg_enabled: true,
      },
    })

    renderDialog()

    expect(await screen.findByText('Enabled')).toBeTruthy()
    expect(screen.getByText('0%')).toBeTruthy()
    expect(screen.queryByText('Remaining amount')).toBeNull()
    expect(screen.queryByText(/Reset at:/)).toBeNull()
    expect(screen.getByText('Enabled')).toBeTruthy()
  })

  test('shows the upstream error and a retry action', async () => {
    vi.mocked(getMistralUsage).mockRejectedValue(new Error('quota denied'))

    renderDialog()

    expect(await screen.findByText('quota denied')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeTruthy()
  })
})
