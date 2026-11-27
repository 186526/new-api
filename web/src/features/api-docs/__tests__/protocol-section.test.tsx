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
import { describe, expect, it } from 'vitest'

import { ProtocolSection } from '../components/protocol-section'
import { API_PROTOCOLS } from '../constants'

function protocol(id: string) {
  const match = API_PROTOCOLS.find((entry) => entry.id === id)
  if (!match) throw new Error(`missing protocol ${id}`)
  return match
}

describe('ProtocolSection', () => {
  it('lists every endpoint of the Moonshot Service group with its method and path', () => {
    render(
      <ProtocolSection
        baseUrl='https://gateway.example.com'
        model='tools-search'
        protocol={protocol('moonshot-service')}
        supportedEndpoint={{}}
      />
    )

    expect(screen.getByText('/moonshot.service/v1/search')).toBeInTheDocument()
    expect(screen.getByText('/moonshot.service/v1/fetch')).toBeInTheDocument()
  })

  it('explains that the fetch answer is markdown rather than JSON', () => {
    render(
      <ProtocolSection
        baseUrl='https://gateway.example.com'
        model='tools-search'
        protocol={protocol('moonshot-service')}
        supportedEndpoint={{}}
      />
    )

    expect(
      screen.getAllByText(/text\/markdown, not JSON/).length
    ).toBeGreaterThan(0)
  })

  it('puts the chosen model into the Gemini path and the Anthropic body', () => {
    const gemini = render(
      <ProtocolSection
        baseUrl='https://gateway.example.com'
        model='gemini-3.8-flash'
        protocol={protocol('gemini')}
        supportedEndpoint={{}}
      />
    )

    expect(
      screen.getByText('/v1beta/models/gemini-3.8-flash:generateContent')
    ).toBeInTheDocument()
    gemini.unmount()

    render(
      <ProtocolSection
        baseUrl='https://gateway.example.com'
        model='claude-sonnet-5-5'
        protocol={protocol('anthropic')}
        supportedEndpoint={{}}
      />
    )

    expect(screen.getByText('claude-sonnet-5-5')).toBeInTheDocument()
  })

  it('switches an endpoint example from curl to Python', async () => {
    const user = userEvent.setup()
    render(
      <ProtocolSection
        baseUrl='https://gateway.example.com'
        model='gpt-6-luna'
        protocol={protocol('openai-chat')}
        supportedEndpoint={{}}
      />
    )

    const [firstPython] = screen.getAllByRole('tab', { name: 'Python' })
    await user.click(firstPython)

    expect(
      await screen.findByText(/from openai import OpenAI/)
    ).toBeInTheDocument()
  })

  it('prefers the path the backend reports over the documented one', () => {
    render(
      <ProtocolSection
        baseUrl='https://gateway.example.com'
        model='claude-sonnet-5-5'
        protocol={protocol('anthropic')}
        supportedEndpoint={{ anthropic: { path: '/v1/messages-v2' } }}
      />
    )

    expect(screen.getByText('/v1/messages-v2')).toBeInTheDocument()
  })
})
