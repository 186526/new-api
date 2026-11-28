import { QueryClientProvider } from '@tanstack/react-query'
import {
  act,
  render,
  renderHook,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { PropsWithChildren } from 'react'
import { toast } from 'sonner'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { createAppQueryClient } from '@/lib/query-client'

import {
  customToolsQueryKey,
  useCustomToolMutation,
} from '../../hooks/use-custom-tool-mutation'
import {
  CustomToolsControl,
  CustomToolsDialog,
} from '../dialogs/custom-tools-dialog'

const search = { configured: true, verified: false, path: '/search' }
const fetch = { configured: false, verified: false }

function renderTools(canConfigure = true, canVerify = true) {
  const client = createAppQueryClient()
  const read = vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { search, fetch } },
  })
  render(
    <QueryClientProvider client={client}>
      <CustomToolsDialog
        channelId={42}
        canConfigure={canConfigure}
        canVerify={canVerify}
        onClose={vi.fn()}
      />
    </QueryClientProvider>
  )
  return read
}

afterEach(() => {
  vi.restoreAllMocks()
})

test('saving a new Fetch configuration sends only the Fetch key and never verifies automatically', async () => {
  renderTools()
  const save = vi.spyOn(api, 'put').mockResolvedValue({
    data: {
      success: true,
      data: { ...fetch, configured: true, path: '/contents' },
    },
  })
  const verify = vi.spyOn(api, 'post')
  const user = userEvent.setup()
  const panel = within(await screen.findByRole('form', { name: 'Fetch' }))
  await user.type(panel.getByLabelText('API Key'), 'fixture-tool-key')
  await user.click(panel.getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(save).toHaveBeenCalledWith(
      '/api/channel/42/custom_tools/fetch',
      { enabled: true, path: '/contents', api_key: 'fixture-tool-key' },
      expect.objectContaining({ singleUseAuthorization: true })
    )
  )
  await waitFor(() =>
    expect(panel.getByRole('button', { name: 'Verify' })).toBeEnabled()
  )
  expect(panel.getByLabelText('API Key')).toHaveValue('')
  expect(verify).not.toHaveBeenCalled()
})

test('channel write can verify a saved operation without editing or revealing its credentials', async () => {
  renderTools(false, true)
  const verify = vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: { ...search, verified: true } },
  })
  const user = userEvent.setup()
  const panel = within(await screen.findByRole('form', { name: 'Search' }))
  expect(panel.queryByLabelText('API Key')).not.toBeInTheDocument()
  expect(panel.queryByRole('button', { name: 'Save' })).not.toBeInTheDocument()
  await user.click(panel.getByRole('button', { name: 'Verify' }))
  expect(await panel.findByText('Verified')).toBeInTheDocument()
  expect(verify).toHaveBeenCalledWith(
    '/api/channel/42/custom_tools/search/verify',
    undefined,
    expect.anything()
  )
})

test.each([409, 200])(
  'a conflict reported with HTTP %i reloads the saved path and retains the draft',
  async (status) => {
    const read = renderTools()
    const user = userEvent.setup()
    const panel = within(await screen.findByRole('form', { name: 'Search' }))
    const path = panel.getByLabelText('Endpoint path')
    expect(path).toHaveAccessibleDescription('Saved path: /search')
    await user.clear(path)
    await user.type(path, '/draft')
    expect(panel.getByRole('button', { name: 'Verify' })).toBeDisabled()

    read.mockResolvedValue({
      data: {
        success: true,
        data: { search: { ...search, path: '/concurrent' }, fetch },
      },
    })
    const save = vi.spyOn(api, 'put')
    if (status === 409) {
      save.mockRejectedValue({ response: { status } })
    } else {
      save.mockResolvedValue({
        data: {
          success: false,
          message_key: 'custom_tool_configuration_changed',
        },
      })
    }
    const notify = vi.spyOn(toast, 'error').mockReturnValue('tool-error')
    await user.type(panel.getByLabelText('API Key'), 'fixture-tool-key')
    expect(panel.getByRole('button', { name: 'Verify' })).toBeDisabled()
    await user.click(panel.getByRole('button', { name: 'Save' }))
    await waitFor(() => {
      expect(path).toHaveAccessibleDescription('Saved path: /concurrent')
      expect(screen.getByRole('status')).toHaveTextContent(
        'Tool configuration changed. Review the saved settings and retry.'
      )
    })
    expect(path).toHaveValue('/draft')
    expect(panel.getByRole('button', { name: 'Verify' })).toBeDisabled()
    expect(save).toHaveBeenCalledWith(
      '/api/channel/42/custom_tools/search',
      { enabled: true, path: '/draft', api_key: 'fixture-tool-key' },
      expect.anything()
    )
    expect(panel.getByLabelText('API Key')).toHaveValue('')
    expect(notify).not.toHaveBeenCalled()
    expect(save).toHaveBeenCalledTimes(1)
  }
)

test('an unsaved channel address hides the configuration entry point', () => {
  render(
    <CustomToolsControl
      channelId={42}
      ready={false}
      disabled={false}
      canConfigure
      canVerify
    />
  )
  expect(
    screen.queryByRole('button', { name: 'Custom tool endpoints' })
  ).not.toBeInTheDocument()
  expect(
    screen.getByText('Save the channel before configuring tool endpoints.')
  ).toBeInTheDocument()
})

test.each([
  ['invalid_custom_tool_credential', 'Enter a valid API key.'],
  ['fixture-tool-key', 'Check the tool path and API key.'],
])(
  'a failed save with reason %s shows exactly one safe notification',
  async (reason, expected) => {
    renderTools()
    const notify = vi.spyOn(toast, 'error').mockReturnValue('tool-error')
    const save = vi.spyOn(api, 'put').mockRejectedValue({
      response: { status: 400, data: { success: false, message: reason } },
    })
    const user = userEvent.setup()
    const panel = within(await screen.findByRole('form', { name: 'Search' }))
    await user.type(panel.getByLabelText('API Key'), 'fixture-tool-key')
    await user.click(panel.getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(panel.getByRole('button', { name: 'Save' })).toBeEnabled()
    )
    expect(notify.mock.calls).toEqual([[expected]])
    expect(panel.getByLabelText('API Key')).toHaveValue('')
    expect(save).toHaveBeenCalledTimes(1)
  }
)

test('unmounting cancels verification and ignores a late success', async () => {
  const client = createAppQueryClient()
  const queryKey = customToolsQueryKey(42)
  const savedTools = { search, fetch }
  client.setQueryData(queryKey, savedTools)
  const verify = vi.spyOn(api, 'post')
  let finish: (() => void) | undefined
  verify.mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = () =>
          resolve({
            data: { success: true, data: { ...search, verified: true } },
          })
      })
  )
  function Wrapper(props: PropsWithChildren) {
    return (
      <QueryClientProvider client={client}>
        {props.children}
      </QueryClientProvider>
    )
  }
  const { result, unmount } = renderHook(() => useCustomToolMutation(42), {
    wrapper: Wrapper,
  })
  let pending: ReturnType<typeof result.current.change> | undefined
  act(() => {
    pending = result.current.change({ operation: 'search', action: 'verify' })
  })
  try {
    await waitFor(() => expect(verify).toHaveBeenCalledTimes(1))
    const signal = verify.mock.calls[0]?.[2]?.signal
    expect(signal?.aborted).toBe(false)

    unmount()
    expect(signal?.aborted).toBe(true)
    finish?.()
    await pending

    expect(client.getQueryData(queryKey)).toEqual(savedTools)
  } finally {
    unmount()
    finish?.()
    await pending
    client.clear()
  }
})

test('confirming removal disables the operation until the single write completes', async () => {
  renderTools()
  let finish: (() => void) | undefined
  const response = new Promise((resolve) => {
    finish = () => resolve({ data: { success: true, data: fetch } })
  })
  const remove = vi
    .spyOn(api, 'put')
    .mockImplementation(
      async () => response as Awaited<ReturnType<typeof api.put>>
    )
  const user = userEvent.setup()
  const panel = within(await screen.findByRole('form', { name: 'Search' }))
  await user.click(panel.getByRole('button', { name: 'Remove' }))
  const confirm = within(await screen.findByRole('alertdialog'))
  await user.click(confirm.getByRole('button', { name: 'Remove' }))
  await waitFor(() => expect(remove).toHaveBeenCalledTimes(1))
  expect(confirm.getByRole('button', { name: 'Remove' })).toBeDisabled()
  finish?.()
  expect(await panel.findByText('Not configured')).toBeInTheDocument()
  expect(panel.getByRole('button', { name: 'Verify' })).toBeDisabled()
  expect(remove).toHaveBeenCalledWith(
    '/api/channel/42/custom_tools/search',
    { enabled: false },
    expect.objectContaining({ singleUseAuthorization: true })
  )
})
