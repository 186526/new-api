import { render, screen } from '@testing-library/react'
import { expect, it } from 'vitest'

import { RequestObservationDetails } from '../components/dialogs/request-observation-details'
import type { LogOtherData } from '../types'

it('shows the refund request without exposing the provider to log owners', () => {
  const tool = {
    contract: 'tools/search',
    cache_hit: true,
    refund: 'requested',
  } as const
  const other: LogOtherData = {
    tool_execution: tool,
    admin_info: { tool_execution: { ...tool, provider: 'codex' } },
  }
  const { rerender } = render(<RequestObservationDetails isAdmin={false} />)
  expect(screen.queryByText('Request observations')).toBeNull()
  rerender(<RequestObservationDetails other={other} isAdmin={false} />)
  expect(screen.getByText('Refund requested')).toBeTruthy()
  expect(screen.queryByText('Refund completed')).toBeNull()
  expect(screen.queryByText('codex')).toBeNull()
  rerender(<RequestObservationDetails other={other} isAdmin />)
  expect(screen.getByText('codex')).toBeTruthy()
})

it('distinguishes an unnecessary refund from a completed refund', () => {
  const other: LogOtherData = JSON.parse(
    '{"tool_execution":{"contract":"tools/search","cache_hit":false,"refund":"not_needed"}}'
  )
  render(<RequestObservationDetails other={other} isAdmin={false} />)
  expect(screen.getByText('Refund not needed')).toBeTruthy()
  expect(screen.queryByText('Refund completed')).toBeNull()
})

it('shows compression time and a recognized reason without rendering raw metadata', () => {
  const image = {
    processed: 0,
    saved_bytes: 0,
    elapsed_ms: 27,
    skipped: 1,
    failed: 0,
    reason: 'budget',
    source_url: 'https://example.invalid/private-image',
  }
  const { rerender } = render(
    <RequestObservationDetails other={{ image_compression: image }} isAdmin />
  )
  expect(screen.getByText('Image compression time (ms)')).toBeTruthy()
  expect(screen.getByText('27')).toBeTruthy()
  expect(screen.getByText('Compression budget exceeded')).toBeTruthy()
  expect(screen.queryByText(image.source_url)).toBeNull()

  rerender(
    <RequestObservationDetails
      other={{ image_compression: { ...image, reason: 'constructor' } }}
      isAdmin
    />
  )
  expect(screen.queryByText('Image compression reason')).toBeNull()
})

it('shows preprocessing separately and preserves a confirmed outbound zero', () => {
  const image = {
    processed: 0,
    saved_bytes: 0,
    elapsed_ms: 0,
    skipped: 0,
    failed: 0,
    preprocessing: {
      processed: 3,
      saved_bytes: 4096,
      elapsed_ms: 27,
      skipped: 1,
      failed: 0,
    },
    outbound_compressed: 0,
  }
  render(
    <RequestObservationDetails other={{ image_compression: image }} isAdmin />
  )
  expect(screen.getByText('Images preprocessed')).toBeTruthy()
  expect(screen.getByText('3')).toBeTruthy()
  expect(screen.getByText('Images compressed').parentElement?.textContent).toBe(
    'Images compressed0'
  )
  expect(screen.getByText('Unavailable')).toBeTruthy()
  expect(screen.queryByText('4,096')).toBeNull()
})

it('shows the recorded reasoning decision, scale, and policy effort', () => {
  const reasoning = { reason: 'applied', effort: 'high', scale: 123 }
  const { rerender } = render(
    <RequestObservationDetails
      other={{ automatic_reasoning: reasoning }}
      isAdmin={false}
    />
  )
  expect(screen.getByText('Policy applied')).toBeTruthy()
  expect(screen.getByText('Reasoning input scale')).toBeTruthy()
  expect(screen.getByText('123')).toBeTruthy()
  expect(screen.getByText('Reasoning policy effort')).toBeTruthy()
  expect(screen.getByText('high')).toBeTruthy()

  rerender(
    <RequestObservationDetails
      other={{
        automatic_reasoning: { ...reasoning, reason: 'explicit_disable' },
      }}
      isAdmin={false}
    />
  )
  expect(screen.getByText('Reasoning explicitly disabled')).toBeTruthy()
  expect(screen.queryByText('Reasoning policy effort')).toBeNull()
})

it('distinguishes unknown outbound reasoning from an observed empty result', () => {
  const reasoning = { reason: 'applied', effort: 'high', scale: 123, tier: 2 }
  const { rerender } = render(
    <RequestObservationDetails
      other={{
        automatic_reasoning: { ...reasoning, outbound_status: 'not_observed' },
      }}
      isAdmin={false}
    />
  )
  expect(screen.getByText('Matched tier')).toBeTruthy()
  expect(
    screen.getByText('Outbound reasoning fields not confirmed')
  ).toBeTruthy()
  rerender(
    <RequestObservationDetails
      other={{
        automatic_reasoning: { ...reasoning, outbound_status: 'observed' },
      }}
      isAdmin={false}
    />
  )
  expect(
    screen.getByText('No outbound reasoning fields returned.')
  ).toBeTruthy()
})
