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
  RouterProvider,
} from '@tanstack/react-router'
import { act, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it } from 'vitest'

import { mapStatusDataToConfig, STATUS_QUERY_KEY } from '@/lib/status-query'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { Footer } from '../components/footer'

let restoreConfig: () => void

beforeEach(() => {
  const originalState = useSystemConfigStore.getState()
  const storageKey = 'system-config-storage'
  const originalStorage = localStorage.getItem(storageKey)
  restoreConfig = () => {
    useSystemConfigStore.setState(originalState, true)
    if (originalStorage === null) {
      localStorage.removeItem(storageKey)
      return
    }
    localStorage.setItem(storageKey, originalStorage)
  }
})

afterEach(() => restoreConfig())

function setFooterHtml(footerHtml: string): void {
  useSystemConfigStore
    .getState()
    .setConfig(mapStatusDataToConfig({ footer_html: footerHtml }))
}

function renderFooter(footerHtml: string): void {
  setFooterHtml(footerHtml)
  const queryClient = new QueryClient()
  queryClient.setQueryData(STATUS_QUERY_KEY, {
    user_agreement_enabled: true,
    privacy_policy_enabled: true,
  })
  const root = createRootRoute({ component: () => <Footer /> })
  const router = createRouter({
    routeTree: root.addChildren([
      createRoute({ getParentRoute: () => root, path: '/' }),
      createRoute({ getParentRoute: () => root, path: '/user-agreement' }),
      createRoute({ getParentRoute: () => root, path: '/privacy-policy' }),
    ]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}

it('preserves ordinary formatting and removes unsupported attributes from configured HTML', async () => {
  const footerHtml =
    '<p><strong footer-note="editor">Service information</strong> <a href="https://example.com/help">Help center</a></p>'
  renderFooter(footerHtml)

  const footer = within(await screen.findByRole('contentinfo'))
  const heading = footer.getByText('Service information', {
    selector: 'strong',
  })
  expect(heading).toBeVisible()
  expect(heading).not.toHaveAttribute('footer-note')
  expect(footer.getByRole('link', { name: 'Help center' })).toHaveAttribute(
    'href',
    'https://example.com/help'
  )
  expect(useSystemConfigStore.getState().config.footerHtml).toBe(footerHtml)
})

it('replaces the previous custom content when the configured HTML changes', async () => {
  renderFooter('<p>Original service notice</p>')
  const footer = within(await screen.findByRole('contentinfo'))
  expect(footer.getByText('Original service notice')).toBeVisible()

  act(() => setFooterHtml('<p><em>Updated service notice</em></p>'))

  expect(
    footer.getByText('Updated service notice', { selector: 'em' })
  ).toBeVisible()
  expect(footer.queryByText('Original service notice')).not.toBeInTheDocument()
})

it('keeps enabled legal links and project attribution alongside custom content', async () => {
  renderFooter('<p>Service information</p>')

  const footer = within(await screen.findByRole('contentinfo'))
  expect(footer.getByRole('link', { name: 'User Agreement' })).toHaveAttribute(
    'href',
    '/user-agreement'
  )
  expect(footer.getByRole('link', { name: 'Privacy Policy' })).toHaveAttribute(
    'href',
    '/privacy-policy'
  )
  expect(footer.getByRole('link', { name: 'New API' })).toHaveAttribute(
    'href',
    'https://github.com/QuantumNous/new-api'
  )
})
