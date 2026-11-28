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
import { Footer } from '@/components/layout/components/footer'
import { usePricingData } from '@/features/pricing/hooks'
import { useStatus } from '@/hooks/use-status'
import { useAuthStore } from '@/stores/auth-store'

import { DocsNav } from './components/docs-nav'
import { KimiCodeConfig } from './components/kimi-code-config'
import { ProtocolSection } from './components/protocol-section'
import { QuickStart } from './components/quick-start'
import { SystemOneBodyExample } from './components/systemone-body-example'
import { ALPHA_SEARCH_ENDPOINT_TYPE, API_PROTOCOLS } from './constants'
import { pickModelsByProtocol, resolveBaseUrl } from './lib/models'

export function ApiDocs({ initialGroup }: { initialGroup?: string } = {}) {
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

  const alphaSearchModel = models?.find((model) =>
    model.supported_endpoint_types?.includes(ALPHA_SEARCH_ENDPOINT_TYPE)
  )?.model_name

  return (
    <PublicLayout showMainContainer={false}>
      <main className='mx-auto w-full max-w-6xl px-4 pt-24 pb-16 sm:px-6 lg:px-8'>
        <header className='grid gap-6 border-b pb-8 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)] lg:gap-12'>
          <div>
            <h1 className='text-3xl leading-tight font-semibold tracking-tight sm:text-4xl'>
              {t('apiDocs.title', { defaultValue: 'API Reference' })}
            </h1>
            <p className='text-muted-foreground mt-3 max-w-md text-sm leading-relaxed'>
              {t('apiDocs.subtitle', {
                defaultValue:
                  'One gateway, several wire protocols. Every example calls this site with your API key.',
              })}
            </p>
          </div>
          <QuickStart baseUrl={baseUrl} />
        </header>

        <div className='grid grid-cols-1 gap-8 pt-8 lg:grid-cols-[12rem_minmax(0,1fr)] lg:gap-10'>
          <DocsNav />

          <div className='flex min-w-0 flex-col gap-10'>
            {API_PROTOCOLS.map((protocol) => (
              <ProtocolSection
                key={protocol.id}
                baseUrl={baseUrl}
                model={modelsByProtocol[protocol.id] ?? protocol.fallbackModel}
                protocol={protocol}
                alphaSearchModel={alphaSearchModel}
                supportedEndpoint={
                  endpointMap as Record<string, { path?: string }>
                }
              >
                {protocol.id === 'systemone' && <SystemOneBodyExample />}
                {protocol.id === 'moonshot-service' && (
                  <KimiCodeConfig baseUrl={baseUrl} initialGroup={initialGroup} />
                )}
              </ProtocolSection>
            ))}
          </div>
        </div>
      </main>
      <Footer />
    </PublicLayout>
  )
}
