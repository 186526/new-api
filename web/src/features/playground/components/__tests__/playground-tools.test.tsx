// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { toolsPlaygroundSearchSchema } from '../../lib/tools-request'
import { PlaygroundTools } from '../playground-tools'

vi.mock('@tanstack/react-router', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@tanstack/react-router')>()),
  Link: (props: { children: React.ReactNode }) => props.children,
}))

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))

const keys = [
  { id: 17, name: 'Trial key', group: 'trial', status: 1 },
  { id: 42, name: 'Standard key', group: 'standard', status: 1 },
  { id: 50, name: 'Disabled key', group: 'trial', status: 2 },
]
const testSecret = 'mock-playground-key-material'
const request = vi.fn<typeof fetch>()

beforeEach(() => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: { items: keys, total: keys.length, page: 1, page_size: 100 },
    },
  })
  vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: { key: testSecret } },
  })
  vi.stubGlobal('fetch', request)
})

afterEach(() => {
  cleanup()
  request.mockReset()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

function show(search = '?mode=tools', fixedChannelId?: number) {
  const options = toolsPlaygroundSearchSchema.parse(
    Object.fromEntries(new URLSearchParams(search))
  )
  const client = new QueryClient({ defaultOptions: { queries: { gcTime: 0 } } })
  render(
    <QueryClientProvider client={client}>
      <PlaygroundTools
        userId={7}
        endpoint={options.endpoint ?? 'moonshot/search'}
        group={options.group ?? ''}
        model={options.model}
        fixedChannelId={fixedChannelId}
      />
    </QueryClientProvider>
  )
}

async function selectTrialKey() {
  await screen.findByRole('option', { name: 'Trial key · trial' })
  fireEvent.change(screen.getByLabelText('API Key'), {
    target: { value: '17' },
  })
}

const paths = [
  {
    path: '/v1/tools/search',
    input: ' example query ',
    payload: { query: 'example query' },
    mediaType: 'application/json',
    body: '{"results":[]}',
  },
  {
    path: '/v1/tools/fetch',
    input: 'https://example.test/',
    payload: { url: 'https://example.test/' },
    mediaType: 'application/json',
    body: '{"content":"example page"}',
  },
  {
    path: '/v1/alpha/search',
    input: 'example query',
    payload: {
      model: 'example-model',
      commands: { search_query: [{ q: 'example query' }] },
    },
    mediaType: 'application/json',
    body: '{"results":[]}',
  },
  {
    path: '/moonshot.service/v1/search',
    input: 'example query',
    payload: { text_query: 'example query' },
    mediaType: 'application/json',
    body: '{"search_results":[]}',
  },
  {
    path: '/moonshot.service/v1/fetch',
    input: 'https://example.test/',
    payload: { url: 'https://example.test/' },
    mediaType: 'text/markdown; charset=utf-8',
    body: '# Example page',
  },
]

describe('Tools Playground', () => {
  it.each(paths)(
    'sends $path only with the explicitly selected key',
    async ({ path, input, payload, mediaType, body }) => {
      show(
        `?mode=tools&endpoint=${encodeURIComponent(path)}&group=trial&keyId=42&model=%20example-model%20`
      )
      await screen.findByRole('option', { name: 'Trial key · trial' })
      expect(
        (screen.getByLabelText('API Key') as HTMLSelectElement).value
      ).toBe('')
      expect(
        screen.queryByRole('option', { name: /Standard key|Disabled key/ })
      ).toBeNull()
      expect(api.post).not.toHaveBeenCalled()
      expect(request).not.toHaveBeenCalled()
      if ('model' in payload) {
        expect((screen.getByLabelText('Model') as HTMLInputElement).value).toBe(
          payload.model
        )
      } else {
        expect(screen.queryByLabelText('Model')).toBeNull()
      }
      await selectTrialKey()
      fireEvent.change(
        screen.getByLabelText('url' in payload ? 'URL' : 'Search query'),
        { target: { value: input } }
      )
      request.mockResolvedValue(
        new Response(body, { headers: { 'Content-Type': mediaType } })
      )
      fireEvent.click(screen.getByRole('button', { name: 'Send request' }))
      await screen.findByText('HTTP 200')
      expect(api.post).toHaveBeenCalledWith('/api/token/17/key')
      expect(request).toHaveBeenCalledExactlyOnceWith(
        path,
        expect.objectContaining({
          method: 'POST',
          credentials: 'omit',
          redirect: 'error',
          headers: {
            Authorization: `Bearer sk-${testSecret}`,
            'Content-Type': 'application/json',
          },
          body: JSON.stringify(payload),
        })
      )
      if (mediaType.startsWith('text/markdown')) {
        expect(
          screen.getByText('# Example page').getAttribute('data-format')
        ).toBe('markdown')
      } else if (!('url' in payload)) {
        expect(screen.getByText('Empty result')).toBeTruthy()
      }
    }
  )

  it('pins an explicit saved-channel test only after the user sends it', async () => {
    show('?mode=tools&group=trial&channelId=999', 42)
    await selectTrialKey()
    expect(api.post).not.toHaveBeenCalled()
    expect(request).not.toHaveBeenCalled()
    fireEvent.change(screen.getByLabelText('Search query'), {
      target: { value: 'example query' },
    })
    request.mockResolvedValue(new Response('{"search_results":[]}', {
      headers: { 'Content-Type': 'application/json' },
    }))
    fireEvent.click(screen.getByRole('button', { name: 'Send request' }))
    await screen.findByText('HTTP 200')
    expect(request).toHaveBeenCalledExactlyOnceWith(
      '/moonshot.service/v1/search',
      expect.objectContaining({
        headers: expect.objectContaining({ Authorization: `Bearer sk-${testSecret}-42` }),
      })
    )
  })

  it('shows a group rejection and masks an echoed bearer without retrying', async () => {
    show()
    await selectTrialKey()
    fireEvent.change(screen.getByLabelText('Search query'), {
      target: { value: 'example query' },
    })
    request.mockResolvedValue(
      new Response(
        JSON.stringify({
          error: {
            code: 'websearch_group_disabled',
            message: `Denied sk-${testSecret}`,
          },
        }),
        { status: 403, headers: { 'Content-Type': 'application/json' } }
      )
    )
    fireEvent.click(screen.getByRole('button', { name: 'Send request' }))
    await screen.findByText('HTTP 403')
    expect(screen.getByText(/Denied \[REDACTED\]/).textContent).not.toContain(
      testSecret
    )
    expect(screen.getByText('Request failed')).toBeTruthy()
    expect(request).toHaveBeenCalledTimes(1)
  })

  it('rejects a non-HTTP fetch before reading the key', async () => {
    show('?mode=tools&endpoint=tools/fetch')
    await selectTrialKey()
    fireEvent.change(screen.getByLabelText('URL'), {
      target: { value: 'ftp://example.test/' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Send request' }))
    await waitFor(() =>
      expect(screen.getByLabelText('URL').getAttribute('aria-invalid')).toBe(
        'true'
      )
    )
    expect(api.post).not.toHaveBeenCalled()
    expect(request).not.toHaveBeenCalled()
  })

  it('falls back to Moonshot Search when a custom request target is supplied', () => {
    show('?mode=tools&endpoint=https://example.test&model=example-model')
    expect((screen.getByLabelText('Endpoint') as HTMLSelectElement).value).toBe(
      'moonshot/search'
    )
    expect(screen.queryByLabelText('Model')).toBeNull()
  })

  it.each(['%20%20', 'x'.repeat(256)])(
    'leaves the model field empty for an invalid Alpha suggestion (%s)',
    async (model) => {
      show(`?mode=tools&endpoint=alpha/search&model=${model}`)
      await selectTrialKey()
      expect((screen.getByLabelText('Model') as HTMLInputElement).value).toBe(
        ''
      )
      fireEvent.change(screen.getByLabelText('Search query'), {
        target: { value: 'example query' },
      })
      fireEvent.click(screen.getByRole('button', { name: 'Send request' }))
      await screen.findByText('Enter an Alpha Search model.')
      expect(api.post).not.toHaveBeenCalled()
      expect(request).not.toHaveBeenCalled()
    }
  )
})
