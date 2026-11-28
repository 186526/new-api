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

import { defaultAutoReasoningPolicy } from '../auto-reasoning-policy'
import { AutoReasoningPreview } from '../auto-reasoning-preview'

const policy = {
  ...defaultAutoReasoningPolicy,
  enabled: true,
  groups: ['draft'],
}
const response = {
  data: {
    success: true,
    data: {
      scale: 42,
      tier: 1,
      tier_effort: 'low',
      effort: 'low',
      reason: 'applied',
      fields: { 'reasoning.effort': 'medium' },
    },
  },
}

async function openPreview() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <AutoReasoningPreview policy={policy} unit='Tokens' />
    </QueryClientProvider>
  )
  await screen.findByRole('option', { name: 'Responses channel (#7)' })
  fireEvent.change(screen.getByLabelText('Channel'), { target: { value: '7' } })
  fireEvent.change(screen.getByLabelText('Group'), {
    target: { value: 'draft' },
  })
  fireEvent.change(screen.getByLabelText('Model name'), {
    target: { value: 'gpt-5' },
  })
}

beforeEach(() => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: [
        { id: -1, name: 'External preset' },
        { id: 7, name: 'Responses channel' },
      ],
    },
  })
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('automatic reasoning channel preview', () => {
  it('calculates the draft tier and retains an explicit effort without selecting a channel or posting', async () => {
    const post = vi.spyOn(api, 'post')
    await openPreview()
    fireEvent.change(screen.getByLabelText('Channel'), { target: { value: '' } })
    fireEvent.change(screen.getByLabelText('Preview input'), { target: { value: 'scale' } })
    fireEvent.change(screen.getByLabelText('Input size (Tokens)'), { target: { value: '1500' } })
    fireEvent.change(screen.getByLabelText('Client reasoning effort'), { target: { value: 'high' } })
    const estimate = screen.getByRole('region', { name: 'Draft reasoning estimate' })
    expect(estimate.textContent).toContain('Matched tier1')
    expect(estimate.textContent).toContain('Effective reasoning efforthigh')
    expect(post).not.toHaveBeenCalled()
  })
  it('uses an existing channel and the unsaved policy to preview text and display final outbound fields', async () => {
    const post = vi.spyOn(api, 'post').mockResolvedValue(response)
    await openPreview()
    expect(screen.queryByRole('option', { name: /External preset/ })).toBeNull()
    fireEvent.change(screen.getByLabelText('Sample text'), {
      target: { value: 'Sample text input' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Run preview' }))
    expect(await screen.findByText('Reasoning preview ready.')).toBeTruthy()
    expect(post).toHaveBeenCalledExactlyOnceWith(
      '/api/system-settings/auto-reasoning/preview',
      {
        policy,
        protocol: 'responses',
        model: 'gpt-5',
        group: 'draft',
        channel_id: 7,
        text: 'Sample text input',
      }
    )
    expect(screen.getByText(/"reasoning\.effort": "medium"/)).toBeTruthy()
    fireEvent.change(screen.getByLabelText('Group'), {
      target: { value: 'new-group' },
    })
    expect(screen.queryByText(/"reasoning\.effort"/)).toBeNull()
  })

  it('accepts a numeric size of zero and announces a failed response without saving the draft', async () => {
    const post = vi.spyOn(api, 'post').mockResolvedValue({
      data: { success: false, message: 'Channel unavailable' },
    })
    await openPreview()
    fireEvent.change(screen.getByLabelText('Preview input'), {
      target: { value: 'scale' },
    })
    fireEvent.change(screen.getByLabelText('Input size (Tokens)'), {
      target: { value: '0' },
    })
    fireEvent.change(screen.getByLabelText('Client reasoning effort'), {
      target: { value: 'none' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Run preview' }))
    await waitFor(() =>
      expect(screen.getByRole('status').textContent).toBe('Channel unavailable')
    )
    expect(post).toHaveBeenCalledExactlyOnceWith(
      '/api/system-settings/auto-reasoning/preview',
      {
        policy,
        protocol: 'responses',
        model: 'gpt-5',
        group: 'draft',
        channel_id: 7,
        scale: 0,
        explicit_effort: 'none',
      }
    )
  })
})
