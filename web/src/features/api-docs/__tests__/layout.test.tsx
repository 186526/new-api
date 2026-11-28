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
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it } from 'vitest'

import { ApiDocs } from '..'
import { QuickStart } from '../components/quick-start'

it('keeps the protocol directory in document order and targets the existing section anchors', async () => {
  const queryClient = new QueryClient()
  queryClient.setQueryData(['status'], {
    server_address: 'https://gateway.example.com',
  })
  queryClient.setQueryData(['notice'], { success: true, data: '' })
  const root = createRootRoute()
  const docs = createRoute({
    getParentRoute: () => root,
    path: '/docs',
    component: ApiDocs,
  })
  const router = createRouter({
    routeTree: root.addChildren([docs]),
    history: createMemoryHistory({ initialEntries: ['/docs#gemini'] }),
  })
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )

  const main = await screen.findByRole('main')
  const directory = within(main).getByRole('navigation', {
    name: 'Protocol navigation',
  })
  expect(directory).toHaveClass('lg:sticky')
  expect(directory.parentElement).toHaveClass(
    'grid-cols-1',
    'lg:grid-cols-[12rem_minmax(0,1fr)]'
  )
  expect(
    within(directory)
      .getAllByRole('link')
      .map((link) => new URL((link as HTMLAnchorElement).href).hash)
  ).toEqual([
    '#openai-chat',
    '#openai-responses',
    '#anthropic',
    '#gemini',
    '#systemone',
    '#moonshot-service',
    '#deepseek-tools',
  ])
  const gemini = within(directory).getByRole('link', { name: /gemini/i })
  const target = within(main).getByRole('region', { name: /gemini/i })
  expect(gemini).toHaveAttribute('aria-current', 'location')
  expect(target).toHaveAttribute('id', 'gemini')
  expect(target).toHaveClass('scroll-mt-24')
  expect(
    directory.compareDocumentPosition(target) & Node.DOCUMENT_POSITION_FOLLOWING
  ).toBeTruthy()
  await user.click(gemini)
  expect(target).toHaveFocus()
  expect(screen.getByRole('contentinfo')).toBeInTheDocument()
})

it('copies the full request even when a long gateway address needs horizontal scrolling', async () => {
  const user = userEvent.setup()
  const gateway = `https://gateway.example.com/${'long-segment-'.repeat(12)}gateway`
  render(<QuickStart baseUrl={gateway} />)

  const example = screen.getByRole('region', { name: 'First request' })
  expect(example).toHaveAttribute('tabindex', '0')
  expect(example).toHaveClass('overflow-x-auto')
  await user.click(screen.getByRole('button', { name: 'Copy first request' }))

  const request = `curl ${gateway}/v1/models \\\n  -H "Authorization: Bearer $NEW_API_KEY"`
  await waitFor(async () =>
    expect(await navigator.clipboard.readText()).toBe(request)
  )
})

it('copies just the gateway address when the base URL copy control is activated', async () => {
  const user = userEvent.setup()
  const gateway = 'https://gateway.example.com'
  render(<QuickStart baseUrl={gateway} />)

  await user.tab()
  expect(screen.getByRole('button', { name: 'Copy base URL' })).toHaveFocus()
  await user.keyboard('{Enter}')

  await waitFor(async () =>
    expect(await navigator.clipboard.readText()).toBe(gateway)
  )
})
