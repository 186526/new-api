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
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  CodeBlock,
  CodeBlockCopyButton,
} from '@/components/ai-elements/code-block'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'

import {
  API_KEY_ENV,
  SAMPLE_LANGUAGE_HIGHLIGHT,
  SAMPLE_LANGUAGE_LABELS,
  SAMPLE_LANGUAGES,
  type ApiSampleLanguage,
} from '../constants'
import { buildEndpointSample, type SampleContext } from '../lib/samples'

type EndpointSamplesProps = {
  endpointId: string
  method: string
  path: string
  title: string
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
  const [language, setLanguage] = useState<ApiSampleLanguage>('curl')

  const context = useMemo<SampleContext>(
    () => ({
      baseUrl: props.baseUrl,
      apiKeyEnv: API_KEY_ENV,
      model: props.model,
      path: props.path,
    }),
    [props.baseUrl, props.model, props.path]
  )

  const code = useMemo(
    () => buildEndpointSample(props.endpointId, language, context),
    [props.endpointId, context, language]
  )

  if (!code) {
    return null
  }

  return (
    <div className='space-y-2'>
      <div className='flex flex-wrap items-center gap-2'>
        <code className='font-mono text-sm font-medium break-all'>
          {props.method} {props.path}
        </code>
        <span className='text-muted-foreground text-xs'>{props.title}</span>
        <Tabs
          value={language}
          onValueChange={(value) => setLanguage(value as ApiSampleLanguage)}
          className='ml-auto'
        >
          <TabsList className='bg-muted/40 h-8 p-0.5'>
            {SAMPLE_LANGUAGES.map((option) => (
              <TabsTrigger
                key={option}
                value={option}
                className='h-7 px-2.5 text-xs'
              >
                {SAMPLE_LANGUAGE_LABELS[option]}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
      </div>
      <CodeBlock code={code} language={SAMPLE_LANGUAGE_HIGHLIGHT[language]}>
        <CodeBlockCopyButton />
      </CodeBlock>
      <p className='text-muted-foreground text-xs'>
        {t('apiDocs.sample.keyHint', {
          defaultValue: 'The examples read your API key from',
        })}{' '}
        <code className='bg-muted rounded px-1 py-0.5 font-mono text-[11px]'>
          {API_KEY_ENV}
        </code>
        .
      </p>
    </div>
  )
}
