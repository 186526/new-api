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
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test } from 'vitest'

import { WebPreview, WebPreviewBody, WebPreviewConsole } from '../web-preview'

test.each([
  { sandbox: undefined },
  { sandbox: 'allow-scripts allow-same-origin' },
])('isolates the preview when the forwarded sandbox is $sandbox', (props) => {
  render(
    <WebPreview defaultUrl='about:blank'>
      <WebPreviewBody name='preview' {...props} />
    </WebPreview>
  )

  const preview = screen.getByTitle('Preview')
  expect(preview).toHaveAttribute('src', 'about:blank')
  expect(preview).toHaveAttribute(
    'sandbox',
    'allow-scripts allow-forms allow-popups allow-presentation'
  )
})

test('keeps the remaining console entry when an identical entry is removed', async () => {
  const user = userEvent.setup()
  const entry = {
    level: 'log' as const,
    message: 'Connected to preview',
    timestamp: new Date('2026-01-01T00:00:00Z'),
  }
  const logs = [
    { ...entry, id: 'first-connection' },
    { ...entry, id: 'second-connection' },
  ]
  const { rerender } = render(
    <WebPreview>
      <WebPreviewConsole logs={logs} />
    </WebPreview>
  )

  await user.click(screen.getByRole('button', { name: 'Console' }))
  const [, remainingEntry] = await screen.findAllByText(entry.message)

  rerender(
    <WebPreview>
      <WebPreviewConsole logs={logs.slice(1)} />
    </WebPreview>
  )

  expect(screen.getByText(entry.message)).toBe(remainingEntry)
})
