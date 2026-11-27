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
import { ToolsProxySettingsSection } from '../tools-proxy-settings-section'

const storedDefaults = {
  'websearch_setting.candidate_order': '',
  'websearch_setting.native_first': true,
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
}

function Fixture(props: FixtureProps) {
  const [container, setContainer] = useState<HTMLDivElement | null>(null)
  return (
    <>
      <div ref={setContainer} />
      <SettingsPageProvider actionsContainer={container}>
        <ToolsProxySettingsSection
          defaultValues={{
            ...storedDefaults,
            ExaKeyConfigured: props.exaKeyConfigured,
          }}
        />
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
  return screen.findByRole('textbox', { name: 'Provider order' })
}

beforeEach(() => {
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
})

test('the page shows the three groups in order', async () => {
  await renderSettings()

  expect(
    screen.getAllByRole('heading', { level: 4 }).map((h) => h.textContent)
  ).toEqual(['Candidate order', 'Cache and retries', 'Upstreams'])
})

test('an unknown provider name is rejected and nothing is saved', async () => {
  const user = userEvent.setup()
  const order = await renderSettings()

  fireEvent.change(order, { target: { value: 'exa,bing' } })
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

test('a retry count above ten is rejected', async () => {
  const user = userEvent.setup()
  await renderSettings()
  const attempts = screen.getByRole('spinbutton', { name: 'Retry attempts' })

  fireEvent.change(attempts, { target: { value: '11' } })
  await user.click(
    screen.getByRole('button', { name: 'Save Tools Proxy settings' })
  )

  await waitFor(() => expect(attempts).toHaveAttribute('aria-invalid', 'true'))
  expect(api.put).not.toHaveBeenCalled()
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
  fireEvent.change(
    screen.getByRole('textbox', { name: 'Antigravity search model' }),
    { target: { value: 'gemini-3-flash' } }
  )
  await user.click(
    screen.getByRole('button', { name: 'Save Tools Proxy settings' })
  )

  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(2))
  expect(api.put).toHaveBeenCalledWith('/api/option/', {
    key: 'websearch_setting.native_first',
    value: false,
  })
  expect(api.put).toHaveBeenCalledWith('/api/option/', {
    key: 'websearch_setting.antigravity_search_model',
    value: 'gemini-3-flash',
  })
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
