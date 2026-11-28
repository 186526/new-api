import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { expect, it, vi } from 'vitest'

import { listSystemInstances } from '../api'
import { SystemInstancesPanel } from '../components/system-instances-panel'
import type { SystemInstance, SystemInstanceListResponse } from '../types'

vi.mock('../api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api')>()),
  listSystemInstances: vi.fn(),
}))

function renderInstances(response: SystemInstanceListResponse) {
  vi.mocked(listSystemInstances).mockResolvedValueOnce(response)
  const queryClient = new QueryClient()
  render(
    <QueryClientProvider client={queryClient}>
      <SystemInstancesPanel />
    </QueryClientProvider>
  )
}

it('shows the local reporting notice when explicitly disabled and the list is empty', async () => {
  renderInstances({
    success: true,
    message: '',
    reporter_enabled: false,
    data: [],
  })

  expect(
    await screen.findByText(/Instance reporting is disabled on this server/)
  ).toBeVisible()
  expect(screen.queryByRole('table')).not.toBeInTheDocument()
})

it('keeps the ordinary empty state when an older server omits the reporter status', async () => {
  renderInstances({ success: true, message: '', data: [] })

  expect(
    await screen.findByText('No instances have reported yet.')
  ).toBeVisible()
})

it('shows remote Pod details even when local reporting is disabled', async () => {
  renderInstances({
    success: true,
    message: '',
    reporter_enabled: false,
    data: [
      {
        node_name: 'pod:example',
        status: 'online',
        started_at: 100,
        last_seen_at: 100,
        stale_after_seconds: 90,
        ephemeral: true,
        host_node_name: 'worker-1',
        info: {
          node: { name: 'relay-0', should_configure_manually: true },
          role: { server_role: 'proxy', region: 'rbx1' },
        },
      },
    ],
  })

  expect(
    await screen.findByRole('region', { name: 'Active Replicas' })
  ).toHaveAttribute('tabindex', '0')
  expect(screen.getByText('relay-0')).toBeVisible()
  expect(screen.getByText('Pod')).toBeVisible()
  expect(screen.getByText('worker-1')).toBeVisible()
  expect(screen.getByText('proxy')).toBeVisible()
  expect(screen.getByText('rbx1')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Configure NODE_NAME' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByText(/Instance reporting is disabled on this server/)
  ).not.toBeInTheDocument()
})

const staleInstance: SystemInstance = {
  node_name: 'old-static',
  status: 'stale',
  started_at: 100,
  last_seen_at: 100,
  stale_after_seconds: 90,
}

it('keeps stale records individually reviewable without offering bulk deletion when none are proven expired', async () => {
  renderInstances({
    success: true,
    message: '',
    data: [
      staleInstance,
      { ...staleInstance, node_name: 'pod:legacy', ephemeral: true },
      {
        ...staleInstance,
        node_name: 'pod:not-yet-expired',
        ephemeral: true,
        expired: false,
      },
    ],
  })

  expect(
    await screen.findAllByRole('button', { name: 'Delete stale instance' })
  ).toHaveLength(3)
  expect(
    screen.queryByRole('button', { name: 'Delete expired records' })
  ).not.toBeInTheDocument()
})

it('offers bulk deletion when the server confirms a temporary record has expired', async () => {
  renderInstances({
    success: true,
    message: '',
    data: [
      staleInstance,
      {
        ...staleInstance,
        node_name: 'pod:expired',
        ephemeral: true,
        expired: true,
      },
    ],
  })

  expect(
    await screen.findByRole('button', { name: 'Delete expired records' })
  ).toBeVisible()
})
