import { AxiosError } from 'axios'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { deleteExpiredSystemInstances } from '../api'

const expiredInstancesEndpoint = '/api/system-info/expired-instances'

afterEach(() => {
  vi.restoreAllMocks()
})

it('returns the deletion count from the dedicated expired instances endpoint', async () => {
  const response = { success: true, message: '', data: { deleted_count: 1 } }
  const remove = vi.spyOn(api, 'delete').mockResolvedValue({ data: response })

  await expect(deleteExpiredSystemInstances()).resolves.toEqual(response)
  expect(remove).toHaveBeenCalledExactlyOnceWith(expiredInstancesEndpoint)
})

it('does not fall back to the legacy route when an older backend returns 404', async () => {
  const notFound = new AxiosError('Request failed with status code 404')
  notFound.status = 404
  const remove = vi.spyOn(api, 'delete').mockRejectedValue(notFound)

  await expect(deleteExpiredSystemInstances()).rejects.toBe(notFound)
  expect(remove).toHaveBeenCalledExactlyOnceWith(expiredInstancesEndpoint)
})
