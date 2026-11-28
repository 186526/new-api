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
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import type { ApiProtocolDoc } from '../constants'
import {
  endpointDescription,
  protocolAuth,
  protocolNote,
  protocolSummary,
  protocolTitle,
} from '../lib/copy'
import { resolveEndpointPath, withModelInPath } from '../lib/models'
import { EndpointSamples } from './endpoint-samples'
import { EndpointTable } from './endpoint-table'

type ProtocolSectionProps = {
  protocol: ApiProtocolDoc
  baseUrl: string
  model: string
  alphaSearchModel?: string
  supportedEndpoint: Record<string, { path?: string }> | undefined
  /** Extra content rendered after the endpoint table, before the samples. */
  children?: ReactNode
}

/**
 * One protocol's whole section: what it is, how it authenticates, its endpoint
 * table, and a runnable example per endpoint.
 */
export function ProtocolSection(props: ProtocolSectionProps) {
  const { t } = useTranslation()

  const endpoints = props.protocol.endpoints.map((endpoint) => ({
    ...endpoint,
    path: withModelInPath(
      resolveEndpointPath(
        endpoint.path,
        endpoint.endpointType,
        props.supportedEndpoint
      ),
      props.model
    ),
    description: endpointDescription(t, endpoint.id),
  }))

  const note = protocolNote(t, props.protocol.id)
  const headingId = `${props.protocol.id}-heading`

  return (
    <section
      id={props.protocol.id}
      aria-labelledby={headingId}
      tabIndex={-1}
      className='flex min-w-0 scroll-mt-24 flex-col gap-5 border-b pb-10 last:border-b-0 last:pb-0'
    >
      <header>
        <h2 id={headingId} className='text-2xl font-semibold tracking-tight'>
          {protocolTitle(t, props.protocol.id)}
        </h2>
        <p className='text-muted-foreground mt-2 text-sm leading-relaxed'>
          {protocolSummary(t, props.protocol.id)}
        </p>
      </header>

      <dl className='grid gap-x-4 gap-y-1 text-sm sm:grid-cols-[max-content_minmax(0,1fr)] [&_dd]:mb-2 sm:[&_dd]:mb-0'>
        <dt className='text-muted-foreground'>
          {t('apiDocs.field.baseUrl', { defaultValue: 'Base URL' })}
        </dt>
        <dd>
          <code className='font-mono text-[13px] wrap-anywhere'>
            {props.baseUrl}
          </code>
        </dd>
        <dt className='text-muted-foreground'>
          {t('apiDocs.field.auth', { defaultValue: 'Authentication' })}
        </dt>
        <dd>{protocolAuth(t, props.protocol.id)}</dd>
        <dt className='text-muted-foreground'>
          {t('apiDocs.field.model', { defaultValue: 'Example model' })}
        </dt>
        <dd>
          <code className='font-mono text-[13px]'>{props.model}</code>
        </dd>
      </dl>

      {note && (
        <p className='text-muted-foreground border-border border-s-2 ps-3 text-sm leading-relaxed'>
          {note}
        </p>
      )}

      <EndpointTable rows={endpoints} labelledBy={headingId} />
      {props.children}

      <div className='flex min-w-0 flex-col gap-5'>
        {endpoints.map((endpoint) => (
          <EndpointSamples
            key={endpoint.id}
            baseUrl={props.baseUrl}
            endpointId={endpoint.id}
            method={endpoint.method}
            model={
              endpoint.id === 'alpha-search'
                ? (props.alphaSearchModel ?? 'YOUR_ALPHA_SEARCH_MODEL')
                : props.model
            }
            path={endpoint.path}
          />
        ))}
      </div>
    </section>
  )
}
