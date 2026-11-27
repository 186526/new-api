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
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { PublicLayout } from '@/components/layout'
import { PageTransition } from '@/components/page-transition'
import { usePricingData } from '@/features/pricing/hooks'
import { useStatus } from '@/hooks/use-status'
import { useAuthStore } from '@/stores/auth-store'

import { DocsNav } from './components/docs-nav'
import { ProtocolSection } from './components/protocol-section'
import { SystemOneBodyExample } from './components/systemone-body-example'
import { API_PROTOCOLS } from './constants'
import { pickModelsByProtocol, resolveBaseUrl } from './lib/models'

export function ApiDocs() {
  const { t } = useTranslation()
  const { status } = useStatus()
  const { auth } = useAuthStore()
  // The page is public, and `/api/pricing` needs a session: asking for it while
  // signed out answers 401 and the shared interceptor sends the visitor to
  // /sign-in. Signed-in visitors get real model names; everyone else sees the
  // documented fallback names from `API_PROTOCOLS`.
  const { models, endpointMap } = usePricingData(Boolean(auth.user))

  const baseUrl = useMemo(() => resolveBaseUrl(status), [status])
  const modelsByProtocol = useMemo(
    () => pickModelsByProtocol(API_PROTOCOLS, models),
    [models]
  )

  return (
    <PublicLayout showMainContainer={false}>
      <PageTransition className='mx-auto w-full max-w-[1200px] px-3 pt-16 pb-12 sm:px-6 sm:pt-20 xl:px-8'>
        <header className='border-b pb-6'>
          <h1 className='text-[clamp(1.75rem,4vw,2.75rem)] leading-tight font-bold tracking-tight'>
            {t('apiDocs.title', { defaultValue: 'API Reference' })}
          </h1>
          <p className='text-muted-foreground mt-2 max-w-2xl text-sm'>
            {t('apiDocs.subtitle', {
              defaultValue:
                'One gateway, several wire protocols. Every example calls this site with your API key.',
            })}
          </p>
        </header>

        <div className='mt-6 grid gap-8 xl:grid-cols-[200px_minmax(0,1fr)]'>
          <aside className='hover-scrollbar sticky top-20 hidden max-h-[calc(100dvh-6rem)] self-start overflow-y-auto xl:block'>
            <DocsNav />
          </aside>

          <main className='min-w-0 space-y-10'>
            {API_PROTOCOLS.map((protocol) => (
              <ProtocolSection
                key={protocol.id}
                baseUrl={baseUrl}
                model={modelsByProtocol[protocol.id] ?? protocol.fallbackModel}
                protocol={protocol}
                supportedEndpoint={
                  endpointMap as Record<string, { path?: string }>
                }
              >
                {protocol.id === 'systemone' ? <SystemOneBodyExample /> : null}
              </ProtocolSection>
            ))}
          </main>
        </div>
      </PageTransition>
    </PublicLayout>
  )
}
