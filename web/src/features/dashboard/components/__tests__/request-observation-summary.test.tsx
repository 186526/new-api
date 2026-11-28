import { render, screen, within } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'

import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { RequestObservationSummary } from '../request-observation-summary'

const observations = {
  tool_requests: 0,
  tool_cache_hits: 0,
  refund_requested: 0,
  refund_completed: 0,
  images_compressed: 0,
  image_bytes_saved: 0,
  redacted_requests: 0,
  redaction_matches: 0,
  automatic_reasoning: 0,
}

afterEach(() => {
  useAuthStore.setState(useAuthStore.getInitialState(), true)
})

it('distinguishes missing savings coverage from a confirmed zero', () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'admin', role: ROLE.ADMIN })
  const { rerender } = render(<RequestObservationSummary data={observations} />)
  const savings = screen.getByText('Image payload bytes saved').parentElement
  if (!savings) {
    throw new Error('Image payload savings metric has no container')
  }
  expect(within(savings).getByText('Unavailable')).toBeTruthy()
  rerender(
    <RequestObservationSummary
      data={{ ...observations, image_savings_samples: 1 }}
    />
  )
  expect(within(savings).getByText('0')).toBeTruthy()
})

it('hides observations from non-administrators', () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'member', role: ROLE.ADMIN - 1 })
  render(<RequestObservationSummary data={observations} />)
  expect(screen.queryByLabelText('Request observations')).toBeNull()
})
