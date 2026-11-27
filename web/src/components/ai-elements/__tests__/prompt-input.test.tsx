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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'

import {
  PromptInput,
  PromptInputAttachments,
  PromptInputProvider,
  PromptInputSubmit,
  PromptInputTextarea,
} from '../prompt-input'

afterEach(() => {
  vi.unstubAllGlobals()
})

test('keeps an attachment available for retry when conversion fails', async () => {
  const user = userEvent.setup()
  const onSubmit = vi.fn()
  const note = new File(['note'], 'note.txt', { type: 'text/plain' })
  vi.stubGlobal(
    'URL',
    Object.assign(class extends URL {}, {
      createObjectURL: () => 'blob:note',
      revokeObjectURL: vi.fn(),
    })
  )
  vi.stubGlobal(
    'fetch',
    vi
      .fn()
      .mockRejectedValueOnce(new Error('Could not read attachment'))
      .mockResolvedValue({ blob: () => Promise.resolve(note) })
  )

  render(
    <PromptInputProvider initialInput='Read the attachment'>
      <PromptInput onSubmit={onSubmit}>
        <PromptInputTextarea />
        <PromptInputAttachments>
          {(attachment) => <span>{attachment.filename}</span>}
        </PromptInputAttachments>
        <PromptInputSubmit />
      </PromptInput>
    </PromptInputProvider>
  )

  await user.upload(screen.getByLabelText('Upload files'), note)
  const submit = screen.getByRole('button', { name: 'Submit' })
  await user.click(submit)

  expect(onSubmit).not.toHaveBeenCalled()
  expect(screen.getByText('note.txt')).toBeInTheDocument()
  expect(screen.getByRole('textbox')).toHaveValue('Read the attachment')

  await user.click(submit)

  await waitFor(() => {
    expect(onSubmit).toHaveBeenCalledWith(
      {
        text: 'Read the attachment',
        files: [
          {
            type: 'file',
            url: 'data:text/plain;base64,bm90ZQ==',
            mediaType: 'text/plain',
            filename: 'note.txt',
          },
        ],
      },
      expect.anything()
    )
    expect(screen.queryByText('note.txt')).not.toBeInTheDocument()
  })
})
