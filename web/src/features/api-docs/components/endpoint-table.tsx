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

import { StaticDataTable } from '@/components/data-table'
import { Badge } from '@/components/ui/badge'

type EndpointRow = {
  id: string
  method: string
  path: string
  description: string
}

type EndpointTableProps = {
  rows: EndpointRow[]
}

/**
 * The path / method / description table of one protocol section. Paths are
 * rendered as code so a reader can copy them without the surrounding prose.
 */
export function EndpointTable(props: EndpointTableProps) {
  const { t } = useTranslation()

  return (
    <StaticDataTable<EndpointRow>
      data={props.rows}
      getRowKey={(row) => row.id}
      columns={[
        {
          id: 'method',
          header: t('apiDocs.table.method', { defaultValue: 'Method' }),
          className: 'w-24',
          cell: (row) => (
            <Badge
              variant={row.method === 'GET' ? 'secondary' : 'outline'}
              className='font-mono text-[11px]'
            >
              {row.method}
            </Badge>
          ),
        },
        {
          id: 'path',
          header: t('apiDocs.table.path', { defaultValue: 'Path' }),
          cell: (row) => (
            <code className='font-mono text-[13px] break-all'>{row.path}</code>
          ),
        },
        {
          id: 'description',
          header: t('apiDocs.table.description', {
            defaultValue: 'Description',
          }),
          className: 'w-1/2',
          cell: (row) => (
            <span className='text-muted-foreground text-sm'>
              {row.description}
            </span>
          ),
        },
      ]}
    />
  )
}
