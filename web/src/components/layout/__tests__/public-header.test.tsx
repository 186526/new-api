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
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from '@tanstack/react-router'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it } from 'vitest'

import { PublicHeader } from '../components/public-header'

function renderPublicHeader(status: Record<string, unknown>) {
  const queryClient = new QueryClient()
  queryClient.setQueryData(['status'], status)
  queryClient.setQueryData(['notice'], { success: true, data: '' })
  const root = createRootRoute({
    component: () => (
      <>
        <PublicHeader
          showAuthButtons={false}
          showLanguageSwitcher={false}
          showThemeSwitch={false}
          showNotifications={false}
        />
        <Outlet />
      </>
    ),
  })
  const home = createRoute({ getParentRoute: () => root, path: '/' })
  const docs = createRoute({ getParentRoute: () => root, path: '/docs' })
  const router = createRouter({
    routeTree: root.addChildren([home, docs]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}

it('shows only the built-in Docs link when external documentation is configured', async () => {
  renderPublicHeader({
    HeaderNavModules: { docs: true },
    docs_link: 'https://manual.example.com',
  })
  const user = userEvent.setup()
  const header = await screen.findByRole('banner')
  const docs = within(header).getByRole('link', { name: 'Docs' })
  expect(docs).toHaveAttribute('href', '/docs')
  expect(docs).not.toHaveAttribute('aria-current')
  expect(
    header.querySelector('a[href="https://manual.example.com"]')
  ).not.toBeInTheDocument()

  await user.click(docs)

  await waitFor(() => expect(docs).toHaveAttribute('aria-current', 'page'))
  expect(
    within(header).getByRole('link', { name: 'Home' })
  ).not.toHaveAttribute('aria-current')
})

it('makes the built-in Docs link available from the mobile menu and marks the current page after navigation', async () => {
  renderPublicHeader({ HeaderNavModules: { docs: false } })
  const user = userEvent.setup()
  const toggle = await screen.findByRole('button', {
    name: 'Toggle navigation menu',
  })
  const menu = document.querySelector<HTMLElement>(
    `[id="${toggle.getAttribute('aria-controls')}"]`
  )
  if (!menu) {
    throw new Error('Missing mobile navigation controlled by the toggle')
  }
  expect(menu).not.toBeVisible()

  await user.click(toggle)
  expect(toggle).toHaveAttribute('aria-expanded', 'true')
  await user.click(within(menu).getByRole('link', { name: 'Docs' }))
  await waitFor(() => expect(menu).not.toBeVisible())

  await user.click(toggle)
  const docs = within(menu).getByRole('link', { name: 'Docs' })
  expect(docs).toHaveAttribute('aria-current', 'page')

  docs.focus()
  await user.keyboard('{Escape}')
  expect(toggle).toHaveFocus()
  expect(toggle).toHaveAttribute('aria-expanded', 'false')
})
