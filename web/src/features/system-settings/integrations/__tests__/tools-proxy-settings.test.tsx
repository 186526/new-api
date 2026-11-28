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
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { toast } from 'sonner'
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { SettingsPageProvider } from '../../components/settings-page-context'
import {
  getOptionValue,
  useSystemOptions,
} from '../../hooks/use-system-options'
import { ToolsProxySettingsSection } from '../tools-proxy-settings-section'

vi.mock('@/features/tools-api/components/ToolsNavigation', () => ({
  ToolsNavigation: () => null,
}))

const storedDefaults = {
  'websearch_setting.candidate_order': '',
  'websearch_setting.native_first': true,
  'websearch_setting.builtin_fallback_enabled': false,
  'websearch_setting.search_cache_ttl_seconds': 600,
  'websearch_setting.fetch_cache_ttl_seconds': 3600,
  'websearch_setting.redirect_cache_ttl_seconds': 86400,
  'websearch_setting.response_header_timeout_seconds': 25,
  'websearch_setting.retry_attempts': 3,
  'websearch_setting.retry_base_delay_milliseconds': 500,
  'websearch_setting.exa_base_url': '',
  'websearch_setting.exa_api_key': '',
  'websearch_setting.antigravity_search_model': '',
}

type FixtureProps = {
  exaKeyConfigured?: boolean
  candidateOrder?: string
}

function Fixture(props: FixtureProps) {
  const [container, setContainer] = useState<HTMLDivElement | null>(null)
  const options = useSystemOptions()
  const defaults = getOptionValue(options.data?.data, {
    ...storedDefaults,
    'websearch_setting.candidate_order': props.candidateOrder ?? '',
    ExaKeyConfigured: props.exaKeyConfigured ?? false,
  })
  return (
    <>
      <div ref={setContainer} />
      <SettingsPageProvider actionsContainer={container}>
        <ToolsProxySettingsSection defaultValues={defaults} />
      </SettingsPageProvider>
    </>
  )
}

async function renderSettings(props: FixtureProps = {}) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <Fixture {...props} />
    </QueryClientProvider>
  )
  return screen.findByRole('combobox', { name: 'Provider order' })
}

beforeEach(() => {
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/option/') return { data: { success: true, data: [] } }
    return { data: { success: false } }
  })
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
})

test('canceling key clearance leaves the stored key and draft unchanged', async () => {
  const user = userEvent.setup()
  await renderSettings({ exaKeyConfigured: true })
  const key = screen.getByLabelText('Exa API key')
  expect(key).toHaveValue('')
  fireEvent.change(key, { target: { value: 'unsaved-key' } })

  screen.getByRole('button', { name: 'Clear Exa key' }).focus()
  await user.keyboard('{Enter}')
  const dialog = screen.getByRole('alertdialog', { name: 'Clear Exa key?' })
  expect(api.put).not.toHaveBeenCalled()
  await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))

  await waitFor(() => expect(dialog).not.toBeVisible())
  expect(key).toHaveValue('unsaved-key')
  expect(api.put).not.toHaveBeenCalled()
})

test('confirming key clearance refreshes status and resets only the key draft', async () => {
  const user = userEvent.setup()
  let configured = true
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url === '/api/option/') {
      return {
        data: {
          success: true,
          data: [{ key: 'ExaKeyConfigured', value: String(configured) }],
        },
      }
    }
    if (url === '/api/tools-api/summary') {
      return {
        data: {
          success: true,
          data: {
            providers: [
              {
                provider: 'exa',
                available_channels: 0,
                global_credential_configured: configured,
                contracts: [],
              },
            ],
          },
        },
      }
    }
    return { data: { success: false } }
  })
  vi.mocked(api.put).mockImplementationOnce(async () => {
    configured = false
    return { data: { success: true } }
  })
  await renderSettings()
  const key = screen.getByLabelText('Exa API key')
  const url = screen.getByRole('textbox', { name: 'Exa base URL' })
  expect(await screen.findByText('Global key configured')).toBeVisible()
  fireEvent.change(key, { target: { value: 'unsaved-key' } })
  fireEvent.change(url, { target: { value: 'https://exa.example' } })

  await user.click(await screen.findByRole('button', { name: 'Clear Exa key' }))
  const dialog = screen.getByRole('alertdialog', { name: 'Clear Exa key?' })
  await user.click(within(dialog).getByRole('button', { name: 'Clear' }))

  expect(api.put).toHaveBeenCalledExactlyOnceWith('/api/option/', {
    key: 'websearch_setting.exa_api_key',
    value: '',
  })
  await waitFor(() => {
    expect(dialog).not.toBeVisible()
    expect(screen.queryByRole('button', { name: 'Clear Exa key' })).toBeNull()
    expect(screen.queryByText('Global key configured')).toBeNull()
  })
  expect(key).toHaveValue('')
  expect(url).toHaveValue('https://exa.example')
})

test('a rejected clear request keeps the key draft and allows retry', async () => {
  const user = userEvent.setup()
  vi.mocked(api.put).mockResolvedValueOnce({
    data: { success: false, message: 'Clear failed' },
  })
  await renderSettings({ exaKeyConfigured: true })
  const key = screen.getByLabelText('Exa API key')
  fireEvent.change(key, { target: { value: 'unsaved-key' } })

  await user.click(screen.getByRole('button', { name: 'Clear Exa key' }))
  const dialog = screen.getByRole('alertdialog', { name: 'Clear Exa key?' })
  const confirm = within(dialog).getByRole('button', { name: 'Clear' })
  await user.click(confirm)

  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  await waitFor(() => expect(confirm).toBeEnabled())
  expect(dialog).toBeVisible()
  expect(key).toHaveValue('unsaved-key')
})

test('the page shows the three groups in order', async () => {
  await renderSettings()

  expect(
    screen.getAllByRole('heading', { level: 4 }).map((h) => h.textContent)
  ).toEqual(['Candidate order', 'Cache and retries', 'Upstreams'])
})

test('a provider absent from the loaded directory is rejected and nothing is saved', async () => {
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url === '/api/option/') return { data: { success: true, data: [] } }
    if (url === '/api/tools-api/summary') {
      return { data: { success: true, data: { providers: [{ provider: 'exa', contracts: [], native_contracts: [], available_channels: 0 }] } } }
    }
    return { data: { success: false } }
  })
  const user = userEvent.setup()
  const order = await renderSettings({ candidateOrder: 'exa,bing' })

  await user.click(
    screen.getByRole('button', { name: 'Save Tools Proxy settings' })
  )

  expect(await screen.findByText('Unknown provider: bing')).toBeVisible()
  expect(order).toHaveAttribute('aria-invalid', 'true')
  expect(api.put).not.toHaveBeenCalled()
})

test('a base URL without a scheme is rejected', async () => {
  const user = userEvent.setup()
  await renderSettings()
  const url = screen.getByRole('textbox', { name: 'Exa base URL' })

  fireEvent.change(url, { target: { value: 'api.exa.ai' } })
  await user.click(
    screen.getByRole('button', { name: 'Save Tools Proxy settings' })
  )

  expect(
    await screen.findByText(
      'Provide a valid URL starting with http:// or https://'
    )
  ).toBeVisible()
  expect(api.put).not.toHaveBeenCalled()
})

test('a disabled cache TTL of -1 is accepted and saved', async () => {
  const user = userEvent.setup()
  await renderSettings()
  const searchTtl = screen.getByRole('spinbutton', {
    name: 'Search cache TTL (seconds)',
  })

  fireEvent.change(searchTtl, { target: { value: '-1' } })
  await user.click(
    screen.getByRole('button', { name: 'Save Tools Proxy settings' })
  )

  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  expect(api.put).toHaveBeenCalledWith('/api/option/', {
    key: 'websearch_setting.search_cache_ttl_seconds',
    value: -1,
  })
})

test('legacy retry settings are displayed as read-only', async () => {
  const user = userEvent.setup()
  await renderSettings()
  await user.click(
    screen.getByRole('button', { name: 'Advanced network & legacy settings' })
  )
  expect(screen.getByRole('spinbutton', { name: 'Retry attempts' })).toHaveAttribute('readonly')
  expect(screen.getByRole('spinbutton', { name: 'Retry base delay (milliseconds)' })).toHaveAttribute('readonly')
})

test('a configured key reads as configured and an empty field is not sent', async () => {
  const user = userEvent.setup()
  const infoToast = vi.spyOn(toast, 'info')
  await renderSettings({ exaKeyConfigured: true })

  const upstreams = screen.getByRole('group', { name: 'Upstreams' })
  expect(
    within(upstreams).getByText(
      'Configured. Leave blank to keep the current key.'
    )
  ).toBeVisible()

  await user.click(
    screen.getByRole('button', { name: 'Save Tools Proxy settings' })
  )

  await waitFor(() =>
    expect(infoToast).toHaveBeenCalledWith('No changes to save')
  )
  expect(api.put).not.toHaveBeenCalled()
})

test('saving sends only the fields that changed', async () => {
  const user = userEvent.setup()
  await renderSettings()

  await user.click(
    screen.getByRole('switch', { name: 'Prefer native providers' })
  )
  expect(screen.getByText('gemini-3.5-flash-lite')).toBeVisible()
  expect(
    screen.queryByRole('textbox', { name: 'Antigravity search model' })
  ).not.toBeInTheDocument()
  await user.click(
    screen.getByRole('button', { name: 'Save Tools Proxy settings' })
  )

  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  expect(api.put).toHaveBeenCalledWith('/api/option/', {
    key: 'websearch_setting.native_first',
    value: false,
  })
})

test('built-in tool fallback starts off and saves only on explicit submission', async () => {
  const user = userEvent.setup()
  await renderSettings()
  const fallback = screen.getByRole('switch', {
    name: 'Enable built-in tool fallback',
  })
  expect(fallback).not.toBeChecked()
  expect(fallback).toHaveAccessibleDescription(/Group policies still apply/)

  fallback.focus()
  await user.keyboard(' ')
  expect(fallback).toBeChecked()
  expect(api.put).not.toHaveBeenCalled()

  const save = screen.getByRole('button', { name: 'Save Tools Proxy settings' })
  await user.click(save)
  await waitFor(() =>
    expect(api.put).toHaveBeenNthCalledWith(1, '/api/option/', {
      key: 'websearch_setting.builtin_fallback_enabled',
      value: true,
    })
  )
  await waitFor(() => expect(save).toBeEnabled())

  await user.click(fallback)
  expect(fallback).not.toBeChecked()
  expect(api.put).toHaveBeenCalledTimes(1)
  await user.click(save)
  await waitFor(() =>
    expect(api.put).toHaveBeenNthCalledWith(2, '/api/option/', {
      key: 'websearch_setting.builtin_fallback_enabled',
      value: false,
    })
  )
  expect(api.put).toHaveBeenCalledTimes(2)
})

test('a typed key is saved once and the field clears afterwards', async () => {
  const user = userEvent.setup()
  await renderSettings()

  const key = screen.getByLabelText('Exa API key')
  fireEvent.change(key, { target: { value: '  sk-exa  ' } })
  await user.click(
    screen.getByRole('button', { name: 'Save Tools Proxy settings' })
  )

  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  expect(api.put).toHaveBeenCalledWith('/api/option/', {
    key: 'websearch_setting.exa_api_key',
    value: 'sk-exa',
  })
  await waitFor(() => expect(key).toHaveValue(''))
})
