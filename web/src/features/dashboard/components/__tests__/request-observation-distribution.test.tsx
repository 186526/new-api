import { render, screen, within } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'

import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { RequestObservationDistribution } from '../request-observation-distribution'

const toolSample = {
  kind: 'tool',
  name: 'tools/search',
  variant: 'codex',
  outcome: 'success',
  count: 7,
}

const reasoningSample = {
  kind: 'reasoning',
  name: '2',
  variant: 'medium',
  outcome: 'applied',
  count: 3,
}

function setRole(role: number) {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'user', role })
}

afterEach(() => {
  useAuthStore.setState(useAuthStore.getInitialState(), true)
})

it('shows tool and reasoning dimensions in separate named tables for administrators', () => {
  setRole(ROLE.ADMIN)
  render(
    <RequestObservationDistribution
      data={[{ ...toolSample, cache_hits: 2 }, reasoningSample]}
    />
  )

  const tools = within(screen.getByRole('table', { name: /tool request/i }))
  expect(tools.getByRole('rowheader', { name: 'tools/search' })).toBeTruthy()
  expect(tools.getByRole('cell', { name: 'codex' })).toBeTruthy()
  expect(tools.getByRole('cell', { name: '7' })).toBeTruthy()
  expect(tools.getByRole('cell', { name: '2' })).toBeTruthy()

  const reasoning = within(screen.getByRole('table', { name: /reasoning/i }))
  expect(reasoning.getByRole('rowheader', { name: '2' })).toBeTruthy()
  expect(reasoning.getByRole('cell', { name: '3' })).toBeTruthy()
  expect(reasoning.queryByRole('columnheader', { name: /cache/i })).toBeNull()
})

it('shows an omitted cache hit count as zero without treating an unknown tier as overflow', () => {
  setRole(ROLE.ADMIN)
  render(
    <RequestObservationDistribution
      data={[toolSample, { ...reasoningSample, name: 'unknown' }]}
    />
  )

  const tools = within(screen.getByRole('table', { name: /tool request/i }))
  expect(tools.getByRole('cell', { name: '0' })).toBeTruthy()
  const reasoning = within(screen.getByRole('table', { name: /reasoning/i }))
  expect(reasoning.getByRole('rowheader', { name: 'Unknown' })).toBeTruthy()
})

it('hides provider distributions from non-administrators even when present in the response', () => {
  setRole(ROLE.ADMIN - 1)
  render(<RequestObservationDistribution data={[toolSample]} />)
  expect(screen.queryByRole('table')).toBeNull()
})

it('distinguishes an explicit empty distribution from a missing response field', () => {
  setRole(ROLE.ADMIN)
  const { rerender } = render(<RequestObservationDistribution />)
  expect(screen.queryByText('No request distribution samples')).toBeNull()

  rerender(<RequestObservationDistribution data={[]} />)
  expect(screen.getByText('No request distribution samples')).toBeTruthy()
})

it('provides a keyboard-focusable scroll region for narrow or long tables', () => {
  setRole(ROLE.ADMIN)
  render(<RequestObservationDistribution data={[toolSample]} />)
  const region = screen.getByRole('region', { name: /tool request/i })
  expect(region.tabIndex).toBe(0)
  expect(region.classList.contains('overflow-auto')).toBe(true)
  region.focus()
  expect(document.activeElement).toBe(region)
})
