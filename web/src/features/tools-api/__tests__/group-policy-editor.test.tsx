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
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { toast } from 'sonner'
import { beforeEach, expect, test, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'

import { ToolsApiContent } from '..'
import { ToolsCache } from '../components/ToolsCache'
import { ToolsDiagnostics } from '../components/ToolsDiagnostics'

const policiesPath = '/api/tools-api/group-policies'
const version = 'fixture-version'
const toolsSearch = '/v1/tools/search'
const toolsFetch = '/v1/tools/fetch'

function renderToolsApi(i18n?: ReturnType<typeof createInstance>) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const router = createRouter({
    routeTree: createRootRoute({ component: ToolsApiContent }),
    history: createMemoryHistory(),
  })
  const app = (
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  render(i18n ? <I18nextProvider i18n={i18n}>{app}</I18nextProvider> : app)
}

function mockOptions(policies: string, groups = ['default', 'premium']) {
  return vi.spyOn(api, 'get').mockImplementation(async (path) => {
    if (path === '/api/group/') return { data: { success: true, data: groups } }
    if (path === policiesPath) {
      return {
        data: { success: true, data: { version, value: policies } },
      }
    }
    throw new Error(`Unexpected GET ${path}`)
  })
}

function getRuleMode(endpoint: string, mode: string) {
  return within(screen.getByRole('group', { name: endpoint })).getByRole(
    'button',
    { name: mode }
  )
}

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => undefined)
})

test('selecting another configured group displays its independent rules', async () => {
  mockOptions(
    JSON.stringify({ premium: { 'tools/search': { mode: 'disabled' } } })
  )
  renderToolsApi()
  const user = userEvent.setup()
  const group = await screen.findByRole('combobox', { name: 'Group' })

  expect(getRuleMode(toolsSearch, 'toolsApi.inherit')).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  await user.selectOptions(group, 'premium')
  expect(getRuleMode(toolsSearch, 'Disabled')).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  expect(getRuleMode(toolsFetch, 'toolsApi.inherit')).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  expect(group).toHaveFocus()
})

test('saving inherit removes only the edited override', async () => {
  mockOptions(
    JSON.stringify({
      default: {
        'tools/search': { mode: 'disabled' },
        'moonshot/fetch': { mode: 'prefer', providers: ['local'] },
      },
    })
  )
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  renderToolsApi()
  await screen.findByRole('combobox', { name: 'Group' })
  const user = userEvent.setup()
  await user.click(getRuleMode(toolsSearch, 'toolsApi.inherit'))
  await user.click(screen.getByRole('button', { name: 'Save changes' }))

  await waitFor(() =>
    expect(put).toHaveBeenCalledWith(policiesPath, {
      version,
      value: JSON.stringify({
        default: { 'moonshot/fetch': { mode: 'prefer', providers: ['local'] } },
      }),
    })
  )
})

test('saving custom provider priority writes the latest rules and refreshes options', async () => {
  let stored = JSON.stringify({
    default: { 'alpha/search': { mode: 'disabled' } },
  })
  const get = vi.spyOn(api, 'get').mockImplementation(async (path) => ({
    data: {
      success: true,
      data:
        path === '/api/group/'
          ? ['default', 'premium']
          : { version, value: stored },
    },
  }))
  const put = vi.spyOn(api, 'put').mockImplementation(async (_path, body) => {
    if (
      !body ||
      typeof body !== 'object' ||
      !('value' in body) ||
      typeof body.value !== 'string'
    ) {
      throw new Error('Expected a string-valued option')
    }
    stored = body.value
    return {
      data: { success: true, data: { value: stored, version: 'saved' } },
    }
  })
  renderToolsApi()
  await screen.findByRole('combobox', { name: 'Group' })
  const user = userEvent.setup()
  await user.click(getRuleMode(toolsSearch, 'toolsApi.prefer'))
  const order = screen.getByRole('combobox', {
    name: `Provider order ${toolsSearch}`,
  })
  expect(
    within(order).queryByRole('option', { name: 'local' })
  ).not.toBeInTheDocument()
  await user.selectOptions(order, 'deepseek')
  await user.selectOptions(order, 'exa')
  await user.click(screen.getByRole('button', { name: 'Move up: exa' }))
  // Another administrator changed a different rule and group after the form loaded.
  stored = JSON.stringify({
    default: { 'alpha/search': { mode: 'prefer', providers: ['codex'] } },
    premium: { 'moonshot/fetch': { mode: 'disabled' } },
  })
  await user.click(screen.getByRole('button', { name: 'Save changes' }))

  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled()
  )
  expect(put).toHaveBeenCalledWith(policiesPath, {
    version,
    value: stored,
  })
  expect(JSON.parse(stored)).toEqual({
    default: {
      'alpha/search': { mode: 'prefer', providers: ['codex'] },
      'tools/search': { mode: 'prefer', providers: ['exa', 'deepseek'] },
    },
    premium: { 'moonshot/fetch': { mode: 'disabled' } },
  })
  expect(get.mock.calls.filter(([path]) => path === policiesPath)).toHaveLength(
    2
  )
})

test('submitting an empty allowlist focuses the invalid field without writing options', async () => {
  mockOptions('{}')
  const put = vi.spyOn(api, 'put')
  renderToolsApi()
  await screen.findByRole('combobox', { name: 'Group' })
  const user = userEvent.setup()
  const only = getRuleMode(toolsSearch, 'Only listed providers')
  only.focus()
  await user.keyboard('[Space]')
  expect(only).toHaveAttribute('aria-pressed', 'true')
  const order = screen.getByRole('combobox', {
    name: `Provider order ${toolsSearch}`,
  })
  await user.click(screen.getByRole('button', { name: 'Save changes' }))

  expect(await screen.findByText('toolsApi.providerRequired')).toBeVisible()
  expect(order).toHaveAttribute('aria-invalid', 'true')
  expect(order).toHaveFocus()
  expect(put).not.toHaveBeenCalled()
})

test('rejected saves show the server message and keep the edited rules', async () => {
  mockOptions('{}')
  vi.spyOn(api, 'put').mockResolvedValue({
    data: { success: false, message: 'Rejected rule' },
  })
  const errorToast = vi.spyOn(toast, 'error')
  renderToolsApi()
  const group = await screen.findByRole('combobox', { name: 'Group' })
  const user = userEvent.setup()
  const disabled = getRuleMode(toolsSearch, 'Disabled')
  await user.click(disabled)
  await user.click(screen.getByRole('button', { name: 'Save changes' }))

  await waitFor(() => expect(errorToast).toHaveBeenCalledWith('Rejected rule'))
  expect(disabled).toHaveAttribute('aria-pressed', 'true')
  expect(group).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Discard changes' }))
  expect(group).toBeEnabled()
})

test('copying a group preserves the strict allowlist mode and provider order', async () => {
  const rule = { mode: 'only', providers: ['codex', 'deepseek'] }
  const premium = { 'tools/search': rule }
  mockOptions(JSON.stringify({ premium }))
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  renderToolsApi()
  const copyGroup = await screen.findByRole('combobox', {
    name: 'Copy rules from group',
  })
  const user = userEvent.setup()
  await user.selectOptions(copyGroup, 'premium')
  expect(getRuleMode(toolsSearch, 'Only listed providers')).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  await user.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith(policiesPath, {
      version,
      value: JSON.stringify({ premium, default: { 'tools/search': rule } }),
    })
  )
})

test('invalid stored rules show the read error and never offer a destructive replacement', async () => {
  mockOptions('{invalid')
  renderToolsApi()

  expect(await screen.findByText('toolsApi.invalidRules')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Save changes' })
  ).not.toBeInTheDocument()
})

test('an empty group response offers no rule editor', async () => {
  mockOptions('{}', [])
  renderToolsApi()

  expect(await screen.findByText('toolsApi.noGroups')).toBeVisible()
  expect(
    screen.queryByRole('combobox', { name: 'Group' })
  ).not.toBeInTheDocument()
})

test('an unavailable directory preserves a saved provider outside the fallback catalog', async () => {
  mockOptions(JSON.stringify({
    default: { 'tools/search': { mode: 'only', providers: ['new-provider'] } },
  }))
  const put = vi.spyOn(api, 'put').mockResolvedValue({
    data: { success: true, data: { value: '{}', version: 'saved' } },
  })
  renderToolsApi()
  const user = userEvent.setup()
  expect(await screen.findByText('Provider directory is unavailable. Existing selections are preserved.')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Remove: new-provider' })).toBeVisible()
  await user.click(getRuleMode(toolsFetch, 'Disabled'))
  await user.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() => expect(put).toHaveBeenCalledWith(policiesPath, {
    version,
    value: JSON.stringify({
      default: {
        'tools/search': { mode: 'only', providers: ['new-provider'] },
        'tools/fetch': { mode: 'disabled' },
      },
    }),
  }))
})

test('a version conflict preserves the draft and does not retry the write', async () => {
  mockOptions('{}')
  const put = vi.spyOn(api, 'put').mockRejectedValue({
    isAxiosError: true,
    response: { status: 409, data: { data: { value: '{}', version: 'new' } } },
  })
  renderToolsApi()
  await screen.findByRole('combobox', { name: 'Group' })
  const user = userEvent.setup()
  await user.click(getRuleMode(toolsSearch, 'Disabled'))
  await user.click(screen.getByRole('button', { name: 'Save changes' }))
  const review = await screen.findByRole('button', { name: 'Keep draft' })
  expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled()
  expect(getRuleMode(toolsSearch, 'Disabled')).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  await user.click(review)
  expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled()
  expect(put).toHaveBeenCalledTimes(1)
})

test('orphan cleanup confirms and saves only the absent groups using CAS', async () => {
  const keep = { 'tools/search': { mode: 'disabled' } }
  mockOptions(JSON.stringify({ retired: keep, default: keep }))
  const put = vi.spyOn(api, 'put').mockResolvedValue({
    data: {
      success: true,
      data: { version: 'new', value: JSON.stringify({ default: keep }) },
    },
  })
  renderToolsApi()
  const user = userEvent.setup()
  await user.click(
    await screen.findByRole('button', { name: /Remove orphan policies/ })
  )
  const dialog = screen.getByRole('alertdialog')
  expect(within(dialog).getByText('retired')).toBeVisible()
  expect(put).not.toHaveBeenCalled()
  await user.click(
    within(dialog).getByRole('button', { name: 'Remove policies' })
  )
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith(policiesPath, {
      version,
      value: JSON.stringify({ default: keep }),
    })
  )
})

test('simulation sends only a dry run and offers the selected contract in Playground', async () => {
  mockOptions('{}')
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        status: 200,
        group: 'premium',
        billing_model: 'tools-search',
        pricing_status: 'expression',
        candidates: [
          { provider: 'codex', native: false, origin: 'channel_upstream' },
        ],
        steps: [],
      },
    },
  })
  renderToolsApi()
  const user = userEvent.setup()
  await user.click(
    await screen.findByRole('button', { name: 'Simulate tool route' })
  )
  const dialog = within(screen.getByRole('dialog'))
  await user.selectOptions(
    dialog.getByRole('combobox', { name: 'Group' }),
    'premium'
  )
  await user.click(dialog.getByRole('button', { name: 'Run simulation' }))
  expect(await dialog.findByText('codex — Channel upstream')).toBeVisible()
  const href =
    dialog
      .getByRole('link', { name: 'Open Playground' })
      .getAttribute('href') ?? ''
  expect(new URL(href, 'http://localhost').searchParams.get('endpoint')).toBe(
    'moonshot/search'
  )
  expect(post).toHaveBeenCalledExactlyOnceWith('/api/tools-api/simulate', {
    group: 'premium',
    contract: 'moonshot/search',
  })
})

test('simulation announces when routing uses the built-in fallback without a channel', async () => {
  mockOptions('{}')
  vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        status: 200,
        group: 'default',
        source: 'builtin_fallback',
        billing_model: 'tools-search',
        pricing_status: 'expression',
        candidates: [
          { provider: 'exa', native: false, origin: 'global_credential' },
        ],
        steps: [{ group: 'default', reason: 'builtin_fallback' }],
      },
    },
  })
  renderToolsApi()
  const user = userEvent.setup()
  await user.click(
    await screen.findByRole('button', { name: 'Simulate tool route' })
  )
  const dialog = within(screen.getByRole('dialog'))
  await user.click(dialog.getByRole('button', { name: 'Run simulation' }))

  expect(
    await dialog.findByText('Route uses built-in fallback without a channel')
  ).toHaveAttribute('role', 'status')
})

test('switching languages translates a completed simulation without submitting it again', async () => {
  mockOptions('{}')
  const i18n = createInstance()
  await i18n.init({
    lng: 'en',
    resources: { en, zhCN: zh },
    fallbackLng: false,
  })
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        status: 503,
        source: 'group_only',
        billing_model: 'tools-search',
        pricing_status: 'missing',
        candidates: [
          { provider: 'codex', native: false, origin: 'channel_upstream' },
        ],
        steps: [
          { group: 'default', reason: 'no_executable_provider' },
          { group: 'premium', reason: 'future_reason' },
        ],
      },
    },
  })
  renderToolsApi(i18n)
  const user = userEvent.setup()
  await user.click(
    await screen.findByRole('button', { name: 'Simulate tool route' })
  )
  const dialog = within(screen.getByRole('dialog'))
  await user.click(dialog.getByRole('button', { name: 'Run simulation' }))

  expect(await dialog.findByText('codex — Channel upstream')).toBeVisible()
  expect(dialog.getByRole('status')).toHaveTextContent(
    'No available route (503)'
  )
  await act(async () => {
    await i18n.changeLanguage('zhCN')
  })
  expect(dialog.getByRole('status')).toHaveTextContent('无可用路由 (503)')
  expect(dialog.getByText('仅限分组指定供应商')).toBeVisible()
  expect(dialog.getByText('未配置价格')).toBeVisible()
  expect(dialog.getByText('codex — 渠道上游')).toBeVisible()
  expect(dialog.getByText('default: 无可用供应商')).toBeVisible()
  expect(dialog.getByText('premium: 未知')).toBeVisible()
  expect(post).toHaveBeenCalledTimes(1)
})

test('cache invalidation sends an exact target only after confirmation', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        scopes: [
          {
            scope: 'forward',
            backend: 'redis',
            coverage: 'shared_redis',
            exact_invalidations: 0,
          },
          {
            scope: 'redirect',
            backend: 'memory',
            coverage: 'local_instance',
            exact_invalidations: 0,
          },
        ],
      },
    },
  })
  const remove = vi
    .spyOn(api, 'delete')
    .mockResolvedValue({ data: { success: true } })
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ToolsCache />
    </QueryClientProvider>
  )
  const user = userEvent.setup()
  expect(await screen.findByText(/Shared across instances/)).toBeVisible()
  expect(screen.getByText(/This instance only/)).toBeVisible()
  await user.selectOptions(
    screen.getByRole('combobox', { name: 'Clear by' }),
    'query'
  )
  await user.type(
    screen.getByRole('textbox', { name: 'Exact query' }),
    'example'
  )
  await user.click(screen.getByRole('button', { name: 'Clear cache' }))
  expect(remove).not.toHaveBeenCalled()
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Clear cache',
    })
  )
  await waitFor(() =>
    expect(remove).toHaveBeenCalledExactlyOnceWith(
      '/api/option/websearch_cache',
      { data: { scope: 'forward', query: 'example' } }
    )
  )
})

test('provider readiness displays the returned mapping and credential states', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        providers: [
          {
            provider: 'codex',
            contracts: ['alpha/search'],
            native_contracts: ['alpha/search'],
            available_channels: 0,
            missing_mapping_channels: 1,
            global_credential_configured: false,
            local_fallback: false,
          },
          {
            provider: 'exa',
            contracts: ['tools/search'],
            native_contracts: [],
            available_channels: 0,
            missing_mapping_channels: 0,
            global_credential_configured: true,
            local_fallback: false,
          },
        ],
      },
    },
  })
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ToolsDiagnostics />
    </QueryClientProvider>
  )
  expect(await screen.findByText('alpha/search · Native')).toBeVisible()
  expect(screen.getByText(/Missing mappings:/)).toBeVisible()
  expect(screen.getByText('Global key configured')).toBeVisible()
})
