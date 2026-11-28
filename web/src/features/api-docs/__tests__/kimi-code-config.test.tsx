import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { fetchTokenKey, getApiKeys } from '@/features/keys/api'
import { useAuthStore } from '@/stores/auth-store'

import { KimiCodeConfig } from '../components/kimi-code-config'

vi.mock('@/features/keys/api', () => ({
  fetchTokenKey: vi.fn(),
  getApiKeys: vi.fn(),
}))

const baseUrl = 'https://gateway.example.com'
const queryClient = new QueryClient()
const initialAuth = useAuthStore.getState().auth

function renderConfig(initialGroup?: string) {
  render(
    <QueryClientProvider client={queryClient}>
      <KimiCodeConfig baseUrl={baseUrl} initialGroup={initialGroup} />
    </QueryClientProvider>
  )
}

function signInWithExampleKey() {
  initialAuth.setUser({ id: 17, username: 'fixture-user', role: 1 })
  vi.mocked(getApiKeys).mockResolvedValue({
    success: true,
    data: {
      items: [{ id: 42, name: 'Browser fixture', group: 'vip', status: 1 }],
      page: 1,
      page_size: 100,
      total: 1,
    },
  } as Awaited<ReturnType<typeof getApiKeys>>)
}

async function selectExampleKey(user: ReturnType<typeof userEvent.setup>) {
  const select = await screen.findByRole('combobox', { name: 'API Key' })
  await user.click(select)
  await user.click(
    await screen.findByRole('option', { name: 'Browser fixture · vip' })
  )
}

afterEach(() => {
  useAuthStore.setState({ auth: initialAuth })
  queryClient.clear()
  vi.resetAllMocks()
})

it('copies the anonymous template for both services without requesting credentials', async () => {
  const user = userEvent.setup()
  renderConfig()

  await user.click(
    screen.getByRole('button', { name: 'Copy Kimi Code template' })
  )

  const expected = [
    '[services.moonshot_search]',
    `base_url = "${baseUrl}/moonshot.service/v1/search"`,
    'api_key = "YOUR_NEW_API_KEY"',
    '',
    '[services.moonshot_fetch]',
    `base_url = "${baseUrl}/moonshot.service/v1/fetch"`,
    'api_key = "YOUR_NEW_API_KEY"',
  ].join('\n')
  expect(await navigator.clipboard.readText()).toBe(expected)
  expect(getApiKeys).not.toHaveBeenCalled()
  expect(fetchTokenKey).not.toHaveBeenCalled()
})

it('reads the chosen credential only when copying and does not render it', async () => {
  const user = userEvent.setup()
  signInWithExampleKey()
  vi.mocked(fetchTokenKey).mockResolvedValue({
    success: true,
    data: { key: 'local-fixture' },
  })
  renderConfig()

  const copy = await screen.findByRole('button', { name: 'Copy with key' })
  expect(copy).toBeDisabled()
  await selectExampleKey(user)
  expect(fetchTokenKey).not.toHaveBeenCalled()
  await user.click(copy)

  await waitFor(async () => {
    const contents = await navigator.clipboard.readText()
    expect(contents.match(/api_key = "sk-local-fixture"/g)).toHaveLength(2)
  })
  expect(fetchTokenKey).toHaveBeenCalledWith(42)
  expect(screen.queryByText(/sk-local-fixture/)).not.toBeInTheDocument()
})

it('lets users clear a linked group that has no enabled keys without reading credentials', async () => {
  const user = userEvent.setup()
  signInWithExampleKey()
  renderConfig('trial')

  const group = await screen.findByRole('combobox', { name: 'Filter keys by group' })
  expect(group).toHaveValue('trial')
  expect(screen.getByRole('button', { name: 'Copy with key' })).toBeDisabled()
  await user.selectOptions(group, '')
  await selectExampleKey(user)
  expect(fetchTokenKey).not.toHaveBeenCalled()
})

it('keeps the selected copy action available after a credential fetch fails', async () => {
  const user = userEvent.setup()
  signInWithExampleKey()
  vi.mocked(fetchTokenKey).mockResolvedValue({ success: false })
  renderConfig()
  await selectExampleKey(user)

  const copy = screen.getByRole('button', { name: 'Copy with key' })
  await user.click(copy)

  expect(await screen.findByText('Failed to load API keys')).toBeInTheDocument()
  expect(copy).toBeEnabled()
})
