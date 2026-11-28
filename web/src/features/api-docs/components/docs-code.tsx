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
import { CodeBlockFrame } from '@/components/ai-elements/code-block'
import { CopyButton } from '@/components/copy-button'

export function DocsCode(props: {
  code: string
  title: string
  copyLabel: string
}) {
  return (
    <CodeBlockFrame
      className='my-0 shadow-none'
      showToolbar
      title={props.title}
      endActions={
        <CopyButton value={props.code} aria-label={props.copyLabel} />
      }
    >
      <pre
        className='focus-visible:outline-ring min-w-0 overflow-x-auto px-3 py-3 font-mono text-xs leading-6 focus-visible:outline-2 focus-visible:-outline-offset-2'
        tabIndex={0}
        role='region'
        aria-label={props.title}
      >
        <code>{props.code}</code>
      </pre>
    </CodeBlockFrame>
  )
}
