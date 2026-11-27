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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { cn } from '@/lib/utils'

import { API_PROTOCOLS } from '../constants'
import { protocolTitle } from '../lib/copy'

/**
 * Left-hand anchor navigation. Every link points at the id of a protocol
 * section on the same page, and the entry for the section in view is marked
 * current so the list follows the reader while scrolling.
 */
export function DocsNav() {
  const { t } = useTranslation()
  const [activeId, setActiveId] = useState<string>(API_PROTOCOLS[0]?.id ?? '')

  useEffect(() => {
    const sections = API_PROTOCOLS.map((protocol) =>
      document.querySelector(`#${protocol.id}`)
    ).filter((element): element is HTMLElement => element !== null)

    if (sections.length === 0) {
      return
    }

    const observer = new IntersectionObserver(
      (entries) => {
        const visible = entries
          .filter((entry) => entry.isIntersecting)
          .sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)
        if (visible[0]?.target.id) {
          setActiveId(visible[0].target.id)
        }
      },
      { rootMargin: '-96px 0px -60% 0px', threshold: 0 }
    )

    sections.forEach((section) => observer.observe(section))
    return () => observer.disconnect()
  }, [])

  return (
    <nav
      aria-label={t('apiDocs.nav.label', {
        defaultValue: 'Protocol navigation',
      })}
      className='text-sm'
    >
      <p className='text-muted-foreground mb-2 px-2 text-xs font-medium tracking-wide uppercase'>
        {t('apiDocs.nav.protocols', { defaultValue: 'Protocols' })}
      </p>
      <ul className='space-y-0.5'>
        {API_PROTOCOLS.map((protocol) => (
          <li key={protocol.id}>
            <a
              aria-current={activeId === protocol.id ? 'true' : undefined}
              className={cn(
                'text-muted-foreground hover:bg-muted hover:text-foreground block rounded-md px-2 py-1.5 transition-colors',
                activeId === protocol.id &&
                  'bg-muted text-foreground font-medium'
              )}
              href={`#${protocol.id}`}
            >
              {protocolTitle(t, protocol.id)}
            </a>
          </li>
        ))}
      </ul>
    </nav>
  )
}
