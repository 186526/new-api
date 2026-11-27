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
import { render, screen } from '@testing-library/react'
import { afterEach, expect, test } from 'vitest'

import { STATUS_QUERY_KEY } from '@/lib/status-query'
import { useAuthStore } from '@/stores/auth-store'

import { Route as ChatRoute } from '../_authenticated/chat/$chatId'

afterEach(() => {
  useAuthStore.getState().auth.reset()
})

test('a web chat preset embeds its resolved URL in a sandbox', async () => {
  const user = { id: 42, username: 'chat-user', role: 1 }
  useAuthStore.getState().auth.setUser(user)
  const client = new QueryClient()
  client.setQueryData(STATUS_QUERY_KEY, {
    chats: [{ 'Web Chat': 'https://chat.example.test/#key={key}' }],
  })
  client.setQueryData(['chat-active-key', user.id], 'sk-fixture')

  const root = createRootRoute()
  const authenticated = createRoute({
    id: '_authenticated',
    getParentRoute: () => root,
  })
  const chatOptions = {
    ...ChatRoute.options,
    getParentRoute: () => authenticated,
    path: '/chat/$chatId',
  }
  const chat = ChatRoute.update(chatOptions)
  const router = createRouter({
    routeTree: root.addChildren([authenticated.addChildren([chat])]),
    history: createMemoryHistory({ initialEntries: ['/chat/0'] }),
  })

  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )

  const iframe = await screen.findByTitle('Chat preset: Web Chat')
  expect(iframe).toHaveAttribute(
    'src',
    'https://chat.example.test/#key=sk-fixture'
  )
  expect(iframe).toHaveAttribute('sandbox', 'allow-forms allow-scripts')

  const openInNewTab = screen.getByRole('link', { name: 'Open in new tab' })
  expect(openInNewTab).toHaveAttribute('href', iframe.getAttribute('src'))
  expect(openInNewTab).toHaveAttribute('target', '_blank')
  expect(openInNewTab).toHaveAttribute('rel', 'noopener noreferrer')
})
