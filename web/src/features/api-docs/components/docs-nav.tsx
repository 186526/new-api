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
import { createLink } from '@tanstack/react-router'
import type { ComponentProps } from 'react'
import { useTranslation } from 'react-i18next'

import { API_PROTOCOLS } from '../constants'
import { protocolTitle } from '../lib/copy'

const ProtocolLink = createLink(function ProtocolAnchor(
  props: ComponentProps<'a'>
) {
  return (
    <a
      {...props}
      aria-current={props['aria-current'] === 'page' ? 'location' : undefined}
    />
  )
})

export function DocsNav() {
  const { t } = useTranslation()

  return (
    <nav
      aria-label={t('apiDocs.nav.label', {
        defaultValue: 'Protocol navigation',
      })}
      className='min-w-0 self-start border-b pb-5 text-sm lg:sticky lg:top-20 lg:max-h-[calc(100dvh-6rem)] lg:overflow-y-auto lg:border-e lg:border-b-0 lg:pe-4 lg:pb-0'
    >
      <p className='text-muted-foreground mb-2 px-2 text-xs font-medium tracking-wide uppercase'>
        {t('apiDocs.nav.protocols', { defaultValue: 'Protocols' })}
      </p>
      <ul className='flex flex-wrap gap-1 lg:flex-col'>
        {API_PROTOCOLS.map((protocol) => (
          <li key={protocol.id}>
            <ProtocolLink
              to='/docs'
              hash={protocol.id}
              activeOptions={{
                exact: true,
                includeHash: true,
                includeSearch: false,
              }}
              onClick={(event) => {
                if (
                  event.defaultPrevented ||
                  event.metaKey ||
                  event.ctrlKey ||
                  event.altKey ||
                  event.shiftKey
                ) {
                  return
                }
                document
                  .querySelector<HTMLElement>(`#${protocol.id}`)
                  ?.focus({ preventScroll: true })
              }}
              className='text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-ring aria-[current=location]:bg-muted aria-[current=location]:text-foreground block rounded-md px-2 py-1.5 focus-visible:outline-2 focus-visible:-outline-offset-2 aria-[current=location]:font-semibold'
            >
              {protocolTitle(t, protocol.id)}
            </ProtocolLink>
          </li>
        ))}
      </ul>
    </nav>
  )
}
