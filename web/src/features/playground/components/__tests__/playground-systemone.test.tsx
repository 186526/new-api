import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { PlaygroundSystemOne } from '../playground-systemone'

const keys = [
  { id: 17, name: 'Trial key', group: 'trial', status: 1 },
  { id: 42, name: 'Standard key', group: 'standard', status: 1 },
  { id: 60, name: 'Other key', group: 'other', status: 1 },
]
const fetchRequest = vi.fn<typeof fetch>()

beforeEach(() => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: { items: keys, total: keys.length, page: 1, page_size: 100 },
    },
  })
  vi.spyOn(api, 'post').mockImplementation(async (url) => ({
    data: { success: true, data: { key: `test-key-${url.split('/')[3]}` } },
  }))
  vi.stubGlobal('fetch', fetchRequest)
})

afterEach(() => {
  cleanup()
  fetchRequest.mockReset()
  vi.unstubAllGlobals()
})

function show() {
  const client = new QueryClient({ defaultOptions: { queries: { gcTime: 0 } } })
  render(
    <QueryClientProvider client={client}>
      <PlaygroundSystemOne userId={7} group='trial' />
    </QueryClientProvider>
  )
  return userEvent.setup()
}

function modelSelect(): HTMLElement {
  return screen.getByRole('combobox', { name: 'Model' })
}

function runButton(): HTMLElement {
  return screen.getByRole('button', { name: 'Run System One' })
}

function modelList(...names: string[]): Response {
  return Response.json({
    models: names.map((name) => ({ name, description: '', release_date: '' })),
  })
}

function deferredModelList() {
  let resolve!: (response: Response) => void
  const promise = new Promise<Response>((resolveResponse) => {
    resolve = resolveResponse
  })
  return { promise, resolve }
}

function submissions() {
  return fetchRequest.mock.calls.filter(([url]) => url === '/systemone/v1/systemone')
}

describe('System One playground', () => {
  it('posts the exact selected discovery name and only the enabled headers on explicit Run', async () => {
    fetchRequest
      .mockResolvedValueOnce(modelList('jev-stable', 'jev-preview'))
      .mockImplementation(async () => Response.json({ model: 'jev-preview' }))
    const user = show()

    await screen.findByRole('option', { name: 'jev-preview' })
    expect(modelSelect()).toHaveValue('jev-stable')
    expect(fetchRequest).toHaveBeenCalledWith(
      '/systemone/v1/models',
      expect.objectContaining({
        headers: expect.objectContaining({ Authorization: 'Bearer sk-test-key-17' }),
      })
    )
    await user.selectOptions(modelSelect(), 'jev-preview')
    const longContext = screen.getByRole('switch', { name: 'Long context' })
    const images = screen.getByRole('switch', { name: 'Images' })
    expect(longContext).not.toBeChecked()
    expect(images).not.toBeChecked()
    await user.click(runButton())
    await screen.findByText('HTTP 200')
    expect(new Headers(submissions()[0][1]?.headers).has('TypeSafe-Danger-Zone-Long-Context')).toBe(false)
    expect(new Headers(submissions()[0][1]?.headers).has('TypeSafe-Danger-Zone-Images')).toBe(false)

    await user.click(longContext)
    await user.tab()
    expect(images).toHaveFocus()
    await user.keyboard('[Space]')
    expect(longContext).toBeChecked()
    expect(images).toBeChecked()
    expect(submissions()).toHaveLength(1)
    await user.click(runButton())
    await screen.findByText('HTTP 200')
    expect(submissions()[1]).toEqual([
      '/systemone/v1/systemone',
      expect.objectContaining({
        method: 'POST',
        headers: {
          Authorization: 'Bearer sk-test-key-17',
          'Content-Type': 'application/json',
          'TypeSafe-Danger-Zone-Long-Context': 'true',
          'TypeSafe-Danger-Zone-Images': 'true',
        },
      }),
    ])
    expect(JSON.parse(String(submissions()[1][1]?.body))).toMatchObject({
      model: 'jev-preview',
    })
  })

  it('preserves a choice only if the next key discovers it and submits with the new key', async () => {
    const standardModels = deferredModelList()
    fetchRequest
      .mockResolvedValueOnce(modelList('jev-trial', 'jev-preview'))
      .mockReturnValueOnce(standardModels.promise)
      .mockResolvedValueOnce(modelList('jev-other'))
      .mockImplementation(async () => Response.json({ model: 'jev-other' }))
    const user = show()
    await screen.findByRole('option', { name: 'jev-preview' })
    await user.selectOptions(modelSelect(), 'jev-preview')

    const keySelect = screen.getByRole('combobox', { name: 'API Key' })
    await user.selectOptions(keySelect, '42')
    expect(screen.getByRole('status')).toHaveTextContent('Loading System One models...')
    expect(modelSelect()).toBeDisabled()
    expect(runButton()).toBeDisabled()
    expect(screen.queryByRole('option', { name: 'jev-preview' })).not.toBeInTheDocument()
    await user.click(runButton())
    expect(submissions()).toHaveLength(0)

    standardModels.resolve(modelList('jev-standard', 'jev-preview'))
    await screen.findByRole('option', { name: 'jev-standard' })
    expect(modelSelect()).toHaveValue('jev-preview')
    expect(fetchRequest).toHaveBeenCalledWith(
      '/systemone/v1/models',
      expect.objectContaining({ headers: { Authorization: 'Bearer sk-test-key-42' } })
    )

    await user.selectOptions(keySelect, '60')
    await screen.findByRole('option', { name: 'jev-other' })
    expect(modelSelect()).toHaveValue('jev-other')
    expect(screen.queryByRole('option', { name: 'jev-preview' })).not.toBeInTheDocument()
    expect(submissions()).toHaveLength(0)
    await user.click(runButton())
    await screen.findByText('HTTP 200')
    expect(new Headers(submissions()[0][1]?.headers).get('Authorization')).toBe('Bearer sk-test-key-60')
    expect(JSON.parse(String(submissions()[0][1]?.body))).toMatchObject({ model: 'jev-other' })
  })

  it('does not display results when an abandoned key lookup resolves late', async () => {
    const trialModels = deferredModelList()
    fetchRequest
      .mockReturnValueOnce(trialModels.promise)
      .mockResolvedValueOnce(modelList('jev-standard'))
    const user = show()
    const keySelect = screen.getByRole('combobox', { name: 'API Key' })
    await waitFor(() => expect(fetchRequest).toHaveBeenCalledTimes(1))
    const trialSignal = fetchRequest.mock.calls[0][1]?.signal
    await user.selectOptions(keySelect, '42')
    await screen.findByRole('option', { name: 'jev-standard' })

    expect(trialSignal?.aborted).toBe(true)
    await act(async () => trialModels.resolve(modelList('jev-trial')))
    expect(modelSelect()).toHaveValue('jev-standard')
    expect(screen.queryByRole('option', { name: 'jev-trial' })).not.toBeInTheDocument()
  })

  it('lists only distinct non-blank model names from the official field', async () => {
    fetchRequest.mockResolvedValueOnce(Response.json({
      models: [{ id: 'jev-id' }, { name: 'jev-valid' }, { name: 'jev-valid' }, { name: ' ' }, { name: 3 }, null],
      data: [{ id: 'jev-openai' }],
    }))
    show()

    await screen.findByRole('option', { name: 'jev-valid' })
    expect(within(modelSelect()).getAllByRole('option')).toHaveLength(1)
    expect(modelSelect()).toHaveValue('jev-valid')
  })

  it.each([
    ['empty list', () => modelList(), 'No System One models available for this API key.'],
    ['legacy response', () => Response.json({ data: [{ id: 'jev-openai' }] }), 'Failed to load System One models.'],
    ['HTTP error', () => new Response(null, { status: 403 }), 'Failed to load System One models.'],
  ])('announces %s without offering a fallback model or sending a request', async (_, response, message) => {
    fetchRequest.mockResolvedValueOnce(response())
    const user = show()

    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent(message))
    expect(modelSelect()).toBeDisabled()
    expect(modelSelect()).toHaveValue('')
    expect(runButton()).toBeDisabled()
    await user.click(runButton())
    expect(submissions()).toHaveLength(0)
  })

  it('can retry a failed discovery without submitting the playground form', async () => {
    fetchRequest
      .mockRejectedValueOnce(new TypeError('Network error'))
      .mockResolvedValueOnce(modelList('jev-recovered'))
    const user = show()

    await user.click(await screen.findByRole('button', { name: 'Retry' }))
    await screen.findByRole('option', { name: 'jev-recovered' })
    expect(runButton()).toBeEnabled()
    expect(submissions()).toHaveLength(0)
  })
})
