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
import { t } from 'i18next'
import { describe, expect, it } from 'vitest'

import { ENDPOINT_TYPES, getEndpointTypeLabels } from '../constants'
import { buildSample, type SampleContext } from '../lib/code-samples'

const BASE_URL = 'https://api.example.com'

function sampleContext(
  endpointType: string,
  endpointPath: string
): SampleContext {
  return {
    baseUrl: BASE_URL,
    apiKeyEnv: 'NEW_API_KEY',
    modelName: 'jev-1.13',
    endpointType,
    endpointPath,
  }
}

describe('Endpoint type labels', () => {
  it('names the System One, search and fetch endpoint types', () => {
    const labels = getEndpointTypeLabels(t)

    expect(labels[ENDPOINT_TYPES.SYSTEMONE]).toBe('System One')
    expect(labels[ENDPOINT_TYPES.MOONSHOT_SEARCH]).toBe('Search')
    expect(labels[ENDPOINT_TYPES.MOONSHOT_FETCH]).toBe('Fetch')
  })

  it('has a label for every declared endpoint type', () => {
    const labels = getEndpointTypeLabels(t)

    for (const value of Object.values(ENDPOINT_TYPES)) {
      expect(labels[value]).toBeTruthy()
    }
  })
})

describe('System One code sample', () => {
  const ctx = sampleContext(ENDPOINT_TYPES.SYSTEMONE, '/systemone/v1/systemone')

  it('posts to the System One path with a choice question that passes validation', () => {
    const curl = buildSample('curl', ctx.endpointType, ctx)

    expect(curl).toContain(`curl ${BASE_URL}/systemone/v1/systemone`)
    expect(curl).toContain('"model": "jev-1.13"')
    expect(curl).toContain('"questions"')
    expect(curl).toContain('"type": "choice"')
    // The upstream takes a criteria object for a choice question, not an
    // options array; a sample with "options" is rejected by the validator.
    expect(curl).toContain('"criteria"')
    expect(curl).not.toContain('"options"')
  })

  it('gives a Python sample that sends the same body', () => {
    const python = buildSample('python', ctx.endpointType, ctx)

    expect(python).toContain('/systemone/v1/systemone')
    expect(python).toContain('"type": "choice"')
    expect(python).toContain('"criteria"')
  })
})

describe('Moonshot service code samples', () => {
  const search = sampleContext(
    ENDPOINT_TYPES.MOONSHOT_SEARCH,
    '/moonshot.service/v1/search'
  )
  const fetch = sampleContext(
    ENDPOINT_TYPES.MOONSHOT_FETCH,
    '/moonshot.service/v1/fetch'
  )

  it('posts text_query to the search path', () => {
    for (const lang of ['curl', 'python'] as const) {
      const sample = buildSample(lang, search.endpointType, search)
      expect(sample).toContain('/moonshot.service/v1/search')
      expect(sample).toContain('text_query')
    }
  })

  it('caps the search result count in the sample', () => {
    // The endpoint answers nine results unless the request asks for fewer, so a
    // sample without the cap teaches a caller to read more than it wanted.
    for (const lang of ['curl', 'python', 'typescript'] as const) {
      const sample = buildSample(lang, search.endpointType, search)
      expect(sample).toContain('num_results')
    }
  })

  it('posts url to the fetch path and says the answer is markdown', () => {
    for (const lang of ['curl', 'python'] as const) {
      const sample = buildSample(lang, fetch.endpointType, fetch)
      expect(sample).toContain('/moonshot.service/v1/fetch')
      expect(sample).toContain('https://example.com')
      expect(sample).toContain('text/markdown')
      expect(sample).toContain('not JSON')
    }
  })
})
