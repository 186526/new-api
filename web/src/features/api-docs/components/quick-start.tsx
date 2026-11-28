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

import { CopyButton } from '@/components/copy-button'

import { API_KEY_ENV } from '../constants'
import { buildEndpointSample } from '../lib/samples'
import { DocsCode } from './docs-code'

export function QuickStart(props: { baseUrl: string }) {
  const { t } = useTranslation()
  const request = buildEndpointSample('models', 'curl', {
    baseUrl: props.baseUrl,
    apiKeyEnv: API_KEY_ENV,
    model: '',
    path: '/v1/models',
  })
  return (
    <div className='flex min-w-0 flex-col gap-2'>
      <div className='border-border flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 border-b pb-2'>
        <span className='text-muted-foreground text-sm'>
          {t('apiDocs.field.baseUrl', { defaultValue: 'Base URL' })}
        </span>
        <code className='min-w-0 flex-1 basis-36 font-mono text-sm wrap-anywhere'>
          {props.baseUrl}
        </code>
        <CopyButton
          value={props.baseUrl}
          aria-label={t('apiDocs.quickStart.copyBaseUrl', {
            defaultValue: 'Copy base URL',
          })}
        />
      </div>
      {request && (
        <DocsCode
          code={request}
          title={t('apiDocs.quickStart.example', {
            defaultValue: 'First request',
          })}
          copyLabel={t('apiDocs.quickStart.copyExample', {
            defaultValue: 'Copy first request',
          })}
        />
      )}
      <p className='text-muted-foreground text-xs'>
        {t('apiDocs.sample.keyHint', {
          defaultValue: 'The examples read your API key from',
        })}{' '}
        <code className='text-foreground font-mono'>{API_KEY_ENV}</code>.
      </p>
    </div>
  )
}
