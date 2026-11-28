import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { batchUpdateApiKeyRedaction, searchApiKeys } from '../../api'

afterEach(() => vi.restoreAllMocks())

test('sends the disabled redaction filter to the server with pagination', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true } })
  await searchApiKeys({ redactionEnabled: false, p: 2, size: 20 })
  expect(get).toHaveBeenCalledWith('/api/token/search?redaction_enabled=false&p=2&size=20')
})

test('batch redaction sends only policy fields and retains individual results', async () => {
  const items = [{ id: 2, redaction_revision: 0, redaction_enabled: false }]
  const result = {
    success: true,
    data: { items: [{ id: 2, success: false, code: 'redaction_revision_conflict' }], updated: 0 },
  }
  const put = vi.spyOn(api, 'put').mockResolvedValue({ data: result })
  expect(await batchUpdateApiKeyRedaction(items)).toEqual(result)
  expect(put).toHaveBeenCalledWith('/api/token/redaction/batch', { items })
})
