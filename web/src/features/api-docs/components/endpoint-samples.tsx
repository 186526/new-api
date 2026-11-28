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
import { useTranslation } from 'react-i18next'

import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

import {
  API_KEY_ENV,
  SAMPLE_LANGUAGES,
  SAMPLE_LANGUAGE_LABELS,
} from '../constants'
import { buildEndpointSample } from '../lib/samples'
import { DocsCode } from './docs-code'

type EndpointSamplesProps = {
  endpointId: string
  method: string
  path: string
  baseUrl: string
  model: string
}

/**
 * One endpoint's runnable examples. Every example uses the same gateway base
 * URL and model name the rest of the page shows, so switching language never
 * changes what the request does.
 */
export function EndpointSamples(props: EndpointSamplesProps) {
  const { t } = useTranslation()
  const context = {
    baseUrl: props.baseUrl,
    apiKeyEnv: API_KEY_ENV,
    model: props.model,
    path: props.path,
  }
  const endpoint = `${props.method} ${props.path}`

  return (
    <Tabs defaultValue='curl' className='min-w-0'>
      <div className='flex min-w-0 flex-wrap items-center justify-between gap-x-3 gap-y-1'>
        <h3 className='min-w-0 font-mono text-sm font-medium wrap-anywhere'>
          {endpoint}
        </h3>
        <TabsList variant='line' aria-label={endpoint}>
          {SAMPLE_LANGUAGES.map((language) => (
            <TabsTrigger
              key={language}
              value={language}
              className='transition-none after:transition-none'
            >
              {SAMPLE_LANGUAGE_LABELS[language]}
            </TabsTrigger>
          ))}
        </TabsList>
      </div>
      {SAMPLE_LANGUAGES.map((language) => {
        const code = buildEndpointSample(props.endpointId, language, context)
        if (!code) return null

        const title = SAMPLE_LANGUAGE_LABELS[language]
        return (
          <TabsContent key={language} value={language} className='min-w-0'>
            <DocsCode
              code={code}
              title={title}
              copyLabel={t('apiDocs.sample.copyEndpoint', {
                defaultValue: 'Copy {{language}} example for {{endpoint}}',
                language: title,
                endpoint,
              })}
            />
          </TabsContent>
        )
      })}
    </Tabs>
  )
}
