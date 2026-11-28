import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useLayoutEffect, useRef, useState } from 'react'

import { handleServerError } from '@/lib/handle-server-error'
import { isServerErrorCancelled } from '@/lib/server-error-message'

import { changeChannelCustomTool } from '../api'
import { channelsQueryKeys } from '../lib/channel-actions'
import {
  CustomToolRequestError,
  type CustomToolChange,
  type CustomToolOperation,
  type CustomTools,
  type CustomToolStatus,
} from '../lib/custom-tools'

export function customToolsQueryKey(channelId: number) {
  return ['channels', 'custom-tools', channelId] as const
}

type ToolFeedback = {
  operation: CustomToolOperation
  result: 'updated' | 'conflict'
}

export function useCustomToolMutation(channelId: number) {
  const client = useQueryClient()
  const queryKey = customToolsQueryKey(channelId)
  const active = useRef<AbortController | null>(null)
  const request = useRef<CustomToolChange | null>(null)
  const [feedback, setFeedback] = useState<ToolFeedback>()
  const [busy, setBusy] = useState(false)

  useLayoutEffect(() => () => active.current?.abort(), [])

  const mutation = useMutation({
    retry: false,
    networkMode: 'always',
    gcTime: 0,
    meta: { errorToast: false },
    mutationFn: async () => {
      const input = request.current
      const signal = active.current?.signal
      request.current = null
      if (!input || !signal) {
        throw new DOMException('No active custom tool request', 'AbortError')
      }
      signal.throwIfAborted()
      await client.cancelQueries({ queryKey, exact: true })
      signal.throwIfAborted()
      return changeChannelCustomTool(channelId, input, signal)
    },
  })

  async function change(
    input: CustomToolChange
  ): Promise<CustomToolStatus | undefined> {
    if (active.current) return
    const controller = new AbortController()
    active.current = controller
    const { operation, action } = input
    request.current = input
    setFeedback(undefined)
    setBusy(true)
    try {
      const updated = await mutation.mutateAsync()
      if (controller.signal.aborted) return
      client.setQueryData<CustomTools>(
        queryKey,
        (previous) => previous && { ...previous, [operation]: updated }
      )
      void client.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
      void client.invalidateQueries({
        queryKey: channelsQueryKeys.detail(channelId),
      })
      setFeedback({ operation, result: 'updated' })
      return updated
    } catch (error) {
      if (controller.signal.aborted || isServerErrorCancelled(error)) return
      const conflict = error instanceof CustomToolRequestError && error.conflict
      if (conflict) setFeedback({ operation, result: 'conflict' })
      else handleServerError(error)
      if (conflict || action === 'verify') {
        await client.invalidateQueries(
          { queryKey, exact: true },
          { cancelRefetch: true }
        )
      }
    } finally {
      request.current = null
      active.current = null
      if (!controller.signal.aborted) {
        mutation.reset()
        setBusy(false)
      }
    }
  }

  return {
    change,
    feedback,
    busy,
    requestInFlight: () => active.current !== null,
  }
}
