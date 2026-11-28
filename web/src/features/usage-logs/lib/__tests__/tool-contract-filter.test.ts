import { describe, expect, it } from 'vitest'

import { buildSearchParams } from '../filter'
import { buildApiParams } from '../utils'

describe('tool contract log filtering', () => {
  it.each([
    'tools/search',
    'tools/fetch',
    'alpha/search',
    'moonshot/search',
    'moonshot/fetch',
  ])('preserves %s in common log requests for both scopes', (toolContract) => {
    const searchParams = buildSearchParams({ toolContract }, 'common')
    for (const isAdmin of [false, true]) {
      expect(buildApiParams({ page: 1, pageSize: 10, searchParams, isAdmin }))
        .toHaveProperty('tool_contract', toolContract)
    }
  })

  it('omits the contract after clearing the filter', () => {
    const searchParams = buildSearchParams({}, 'common')
    expect(buildApiParams({ page: 1, pageSize: 10, searchParams, isAdmin: false }))
      .not.toHaveProperty('tool_contract')
  })
})
