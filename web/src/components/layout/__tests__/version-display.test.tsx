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
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { cleanup, render, screen, within } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, expect, it, vi } from 'vitest'

import { SidebarProvider } from '@/components/ui/sidebar'
import { ROLE } from '@/lib/roles'
import { STATUS_QUERY_KEY } from '@/lib/status-query'
import { useAuthStore } from '@/stores/auth-store'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { AppHeader } from '../components/app-header'
import { PublicHeader } from '../components/public-header'
import { SystemBrand } from '../components/system-brand'

const queryClient = new QueryClient()

function renderLayout(content: ReactNode, version?: string) {
  queryClient.setQueryData(STATUS_QUERY_KEY, {
    system_name: 'Test API',
    version,
  })
  queryClient.setQueryData(['notice'], { success: true, data: '' })
  const router = createRouter({
    routeTree: createRootRoute({ component: () => content }),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}

afterEach(() => {
  cleanup()
  queryClient.clear()
  useAuthStore.getState().auth.reset()
  useSystemConfigStore.getState().setLoading(true)
  useSystemConfigStore.persist.clearStorage()
  vi.unstubAllGlobals()
})

it.each([
  {
    name: 'app',
    header: (
      <SidebarProvider>
        <AppHeader rightContent={false} />
      </SidebarProvider>
    ),
  },
  {
    name: 'public',
    header: (
      <PublicHeader
        siteName='Test API'
        showAuthButtons={false}
        showLanguageSwitcher={false}
        showThemeSwitch={false}
        showNotifications={false}
      />
    ),
  },
])(
  'the $name header omits the version and update control for administrators',
  async ({ header }) => {
    useSystemConfigStore.getState().setLoading(false)
    useAuthStore
      .getState()
      .auth.setUser({ id: 1, username: 'admin', role: ROLE.ADMIN })
    const fetchRelease = vi.fn(async () => new Response('[]'))
    vi.stubGlobal('fetch', fetchRelease)
    renderLayout(header, 'v1.2.3')

    const banner = within(await screen.findByRole('banner'))
    expect(banner.getByText('Test API')).toBeInTheDocument()
    expect(banner.queryByText('v1.2.3')).not.toBeInTheDocument()
    expect(
      banner.queryByRole('button', {
        name: /system updates|check for updates/i,
      })
    ).not.toBeInTheDocument()
    expect(fetchRelease).not.toHaveBeenCalled()
  }
)

it('sidebar branding omits the unknown version fallback when status has no version', async () => {
  renderLayout(
    <SidebarProvider>
      <SystemBrand />
    </SidebarProvider>
  )

  expect(await screen.findByText('Test API')).toBeInTheDocument()
  expect(screen.queryByText('Unknown version')).not.toBeInTheDocument()
})
