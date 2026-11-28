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
import { expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { formatTimestamp } from '@/lib/format'
import { STATUS_QUERY_KEY } from '@/lib/status-query'

import { PerformanceSection } from '../performance-section'

const startTimestamp = 1_700_000_000

it.each([
  [startTimestamp, formatTimestamp(startTimestamp)],
  [null, 'Unknown'],
])(
  'shows the server uptime on Performance for start time %s',
  async (startTime, uptime) => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: {} },
    })
    const client = new QueryClient()
    client.setQueryData(STATUS_QUERY_KEY, { start_time: startTime })
    const view = render(
      <QueryClientProvider client={client}>
        <PerformanceSection
          defaultValues={{
            'performance_setting.disk_cache_enabled': false,
            'performance_setting.disk_cache_threshold_mb': 10,
            'performance_setting.disk_cache_max_size_mb': 1024,
            'performance_setting.disk_cache_path': '',
            'performance_setting.monitor_enabled': false,
            'performance_setting.monitor_cpu_threshold': 90,
            'performance_setting.monitor_memory_threshold': 90,
            'performance_setting.monitor_disk_threshold': 95,
          }}
        />
      </QueryClientProvider>
    )

    try {
      expect(await screen.findByText('Uptime since')).toBeVisible()
      expect(screen.getByText(uptime)).toBeVisible()
    } finally {
      view.unmount()
      client.clear()
    }
  }
)
