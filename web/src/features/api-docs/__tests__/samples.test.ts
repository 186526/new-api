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
import { describe, expect, it } from 'vitest'

import { API_PROTOCOLS } from '../constants'
import {
  pickModelsByProtocol,
  resolveBaseUrl,
  resolveEndpointPath,
  withModelInPath,
} from '../lib/models'
import { buildEndpointSample } from '../lib/samples'

const sampleContext = {
  baseUrl: 'https://gateway.example.com',
  apiKeyEnv: 'NEW_API_KEY',
  model: 'test-model',
  path: '/v1/chat/completions',
}

describe('resolveBaseUrl', () => {
  it('reads server_address and drops the trailing slash', () => {
    expect(
      resolveBaseUrl({ server_address: 'https://gateway.example.com/' })
    ).toBe('https://gateway.example.com')
  })

  it('falls back to the origin when status has no address', () => {
    const fallback = resolveBaseUrl(null)
    expect(fallback.startsWith('http')).toBe(true)
  })
})

describe('pickModelsByProtocol', () => {
  it('uses the real model name when the catalog lists one for the protocol', () => {
    const picked = pickModelsByProtocol(API_PROTOCOLS, [
      {
        id: 1,
        model_name: 'real-chat-model',
        quota_type: 0,
        model_ratio: 1,
        completion_ratio: 1,
        enable_groups: [],
        supported_endpoint_types: ['openai'],
      },
    ])

    expect(picked['openai-chat']).toBe('real-chat-model')
  })

  it('keeps the documented fallback for a protocol the catalog does not serve', () => {
    const picked = pickModelsByProtocol(API_PROTOCOLS, [])
    const systemOne = API_PROTOCOLS.find((p) => p.id === 'systemone')

    expect(picked['systemone']).toBe(systemOne?.fallbackModel)
  })
})

describe('resolveEndpointPath', () => {
  it('prefers the path the backend reports for the endpoint type', () => {
    expect(
      resolveEndpointPath('/v1/chat/completions', 'openai', {
        openai: { path: '/v1/moved' },
      })
    ).toBe('/v1/moved')
  })

  it('keeps the documented path when the backend reports none', () => {
    expect(resolveEndpointPath('/v1/messages', 'anthropic', {})).toBe(
      '/v1/messages'
    )
  })
})

describe('withModelInPath', () => {
  it('replaces the model placeholder in a Gemini path', () => {
    expect(
      withModelInPath('/v1beta/models/{model}:generateContent', 'gemini-x')
    ).toBe('/v1beta/models/gemini-x:generateContent')
  })
})

describe('buildEndpointSample', () => {
  it('builds a chat completion curl that posts to the documented path', () => {
    const sample = buildEndpointSample(
      'chat-completions',
      'curl',
      sampleContext
    )

    expect(sample).toContain('https://gateway.example.com/v1/chat/completions')
    expect(sample).toContain('Authorization: Bearer $NEW_API_KEY')
    expect(sample).toContain('"model": "test-model"')
  })

  it('builds a chat completion python sample that imports os before reading the key', () => {
    const sample = buildEndpointSample(
      'chat-completions',
      'python',
      sampleContext
    )
    const lines = sample?.split('\n') ?? []

    expect(lines[0]).toBe('import os')
    expect(lines.indexOf('from openai import OpenAI')).toBeGreaterThan(0)
  })

  it('documents the System One request body with model, state and questions', () => {
    const sample = buildEndpointSample(
      'systemone-decide',
      'curl',
      sampleContext
    )

    expect(sample).toContain('"model":')
    expect(sample).toContain('"state":')
    expect(sample).toContain('"questions":')
    expect(sample).toContain('"type": "choice"')
  })

  it('describes the Moonshot fetch answer as text rather than JSON', () => {
    const sample = buildEndpointSample('moonshot-fetch', 'curl', sampleContext)

    expect(sample).toContain('text/markdown')
    expect(sample).not.toContain('json.loads')
  })

  it.each([
    [
      'tools-search',
      '/v1/tools/search',
      { query: expect.any(String), count: 10 },
    ],
    ['tools-fetch', '/v1/tools/fetch', { url: 'https://example.com/' }],
    [
      'alpha-search',
      '/v1/alpha/search',
      {
        model: 'test-model',
        commands: { search_query: [{ q: expect.any(String) }] },
      },
    ],
    [
      'moonshot-search',
      '/moonshot.service/v1/search',
      { text_query: expect.any(String), num_results: 5 },
    ],
    [
      'moonshot-fetch',
      '/moonshot.service/v1/fetch',
      { url: 'https://example.com/' },
    ],
  ] as const)(
    'builds matching curl and Python requests for %s',
    (id, path, body) => {
      const context = { ...sampleContext, path }
      const curl = buildEndpointSample(id, 'curl', context)
      const python = buildEndpointSample(id, 'python', context)
      const url = `${sampleContext.baseUrl}${path}`
      expect(curl).toContain(url)
      expect(python).toContain(url)
      expect(python).toContain('response.raise_for_status()')
      expect(JSON.parse(curl?.match(/-d '([\s\S]*)'$/)?.[1] ?? '')).toEqual(
        body
      )
      expect(JSON.parse(python?.match(/json=(.*),/)?.[1] ?? '')).toEqual(body)
    }
  )

  it('returns null for an endpoint without a written sample', () => {
    expect(
      buildEndpointSample('unknown-endpoint', 'curl', sampleContext)
    ).toBeNull()
  })
})
