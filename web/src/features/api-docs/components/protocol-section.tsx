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
import { resolveEndpointPath } from '../lib/models'
import { EndpointSamples } from './endpoint-samples'
import { EndpointTable } from './endpoint-table'

type ProtocolSectionProps = {
  protocol: ApiProtocolDoc
  baseUrl: string
  model: string
  supportedEndpoint: Record<string, { path?: string }> | undefined
  /** Extra content rendered after the note, before the endpoint table. */
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
    path: resolveEndpointPath(
      endpoint.path,
      endpoint.endpointType,
      props.supportedEndpoint
    ),
  }))

  const note = protocolNote(t, props.protocol.id)

  return (
    <section
      id={props.protocol.id}
      className='scroll-mt-24 border-b pb-10 last:border-b-0'
    >
      <h2 className='text-2xl font-semibold tracking-tight'>
        {protocolTitle(t, props.protocol.id)}
      </h2>
      <p className='text-muted-foreground mt-2 text-sm'>
        {protocolSummary(t, props.protocol.id)}
      </p>

      <dl className='mt-4 grid gap-2 text-sm sm:grid-cols-[7rem_minmax(0,1fr)]'>
        <dt className='text-muted-foreground'>
          {t('apiDocs.field.baseUrl', { defaultValue: 'Base URL' })}
        </dt>
        <dd>
          <code className='font-mono text-[13px] break-all'>
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

      {note ? (
        <p className='bg-muted/40 mt-4 rounded-lg border px-3 py-2 text-sm'>
          {note}
        </p>
      ) : null}

      {props.children}

      <div className='mt-6'>
        <EndpointTable
          rows={endpoints.map((endpoint) => ({
            id: endpoint.id,
            method: endpoint.method,
            path: endpoint.path.replace('{model}', props.model),
            description: endpointDescription(t, endpoint.id),
          }))}
        />
      </div>

      <div className='mt-6 space-y-8'>
        {endpoints.map((endpoint) => (
          <EndpointSamples
            key={endpoint.id}
            baseUrl={props.baseUrl}
            endpointId={endpoint.id}
            method={endpoint.method}
            model={props.model}
            path={endpoint.path.replace('{model}', props.model)}
            title={endpointDescription(t, endpoint.id)}
          />
        ))}
      </div>
    </section>
  )
}
