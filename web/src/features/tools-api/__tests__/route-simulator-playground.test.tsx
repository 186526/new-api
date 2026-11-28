// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { toolsPlaygroundSearchSchema } from '@/features/playground/lib/tools-request'
import { api } from '@/lib/api'

import { RouteSimulator } from '../components/RouteSimulator'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key, i18n: { language: 'en' } }),
}))

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

it('links the submitted Alpha model and group without forwarding simulation-only fields', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { items: [{ id: 17, type: 57, name: 'Fixture' }], page_size: 100, total: 1 } },
  })
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        status: 200,
        billing_model: 'different-billing-model',
        channels: [
          {
            group: 'trial',
            channel_id: 17,
            channel_name: 'Fixture',
            matched_model: 'example/model+preview',
            priority: 7,
            weight: 40,
            first_attempt: true,
            providers: [{ provider: 'codex', native: true, origin: 'native_channel' }],
          },
        ],
        eligibility: [
          { group: 'trial', channel_id: 17, provider: 'exa', reason: 'missing_credential' },
        ],
      },
    },
  })
  const client = new QueryClient()
  const root = createRootRoute({
    component: () => (
      <RouteSimulator groups={['trial']} selectedGroup='trial' />
    ),
  })
  const playground = createRoute({
    getParentRoute: () => root,
    path: '/playground',
    validateSearch: toolsPlaygroundSearchSchema,
  })
  const router = createRouter({
    routeTree: root.addChildren([playground]),
    history: createMemoryHistory(),
  })
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )

  await user.click(
    await screen.findByRole('button', { name: 'Simulate tool route' })
  )
  const dialog = within(screen.getByRole('dialog'))
  await user.selectOptions(dialog.getByLabelText('Endpoint'), 'alpha/search')
  await user.type(dialog.getByLabelText('Model'), '  example/model+preview  ')
  await dialog.findByRole('option', { name: 'Fixture · ChatGPT Subscription (Codex) (#17)' })
  await user.selectOptions(dialog.getByLabelText('Channel (optional)'), '17')
  expect(post).not.toHaveBeenCalled()
  await user.click(dialog.getByRole('button', { name: 'Run simulation' }))
  const link = await dialog.findByRole('link', { name: 'Open Playground' })
  const table = within(dialog.getByRole('table', { name: 'Channels' }))
  const candidate = within(table.getByRole('row', { name: /Fixture/ }))
  expect(candidate.getByRole('rowheader', { name: 'Fixture (#17)' })).toBeTruthy()
  expect(candidate.getByRole('cell', { name: 'First attempt' })).toBeTruthy()
  expect(dialog.getByText(/Missing provider credential/)).toBeTruthy()

  await user.clear(dialog.getByLabelText('Model'))
  await user.type(dialog.getByLabelText('Model'), 'unsubmitted-edit')
  const url = new URL(link.getAttribute('href') ?? '', 'http://localhost')
  expect(url.pathname).toBe('/playground')
  expect(Object.fromEntries(url.searchParams)).toEqual({
    mode: 'tools',
    endpoint: 'alpha/search',
    group: 'trial',
    model: 'example/model+preview',
  })
  expect(post).toHaveBeenCalledExactlyOnceWith('/api/tools-api/simulate', {
    group: 'trial',
    contract: 'alpha/search',
    channel_id: 17,
    model: 'example/model+preview',
  })
})
