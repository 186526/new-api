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

import { Badge } from '@/components/ui/badge'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

type EndpointRow = {
  id: string
  method: string
  path: string
  description: string
}

type EndpointTableProps = {
  rows: EndpointRow[]
  labelledBy: string
}

/**
 * The path / method / description table of one protocol section. Paths are
 * rendered as code so a reader can copy them without the surrounding prose.
 */
export function EndpointTable(props: EndpointTableProps) {
  const { t } = useTranslation()

  return (
    <Table
      aria-labelledby={props.labelledBy}
      withContainer={false}
      className='table-fixed border-y'
    >
      <TableHeader>
        <TableRow className='transition-none'>
          <TableHead
            scope='col'
            className='w-17 wrap-anywhere whitespace-normal sm:w-20'
          >
            {t('apiDocs.table.method', { defaultValue: 'Method' })}
          </TableHead>
          <TableHead scope='col' className='wrap-anywhere whitespace-normal'>
            {t('apiDocs.table.path', { defaultValue: 'Path' })}
          </TableHead>
          <TableHead
            scope='col'
            className='w-2/5 wrap-anywhere whitespace-normal'
          >
            {t('apiDocs.table.description', { defaultValue: 'Description' })}
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody className='[&>tr]:h-auto'>
        {props.rows.map((row) => (
          <TableRow key={row.id} className='transition-none'>
            <TableCell className='align-top'>
              <Badge variant={row.method === 'GET' ? 'secondary' : 'outline'}>
                {row.method}
              </Badge>
            </TableCell>
            <TableHead
              scope='row'
              className='h-auto py-2 align-top wrap-anywhere whitespace-normal'
            >
              <code className='font-mono!'>{row.path}</code>
            </TableHead>
            <TableCell className='text-muted-foreground align-top wrap-anywhere whitespace-normal'>
              {row.description}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
