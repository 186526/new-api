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

import { DocsCode } from './docs-code'

const SYSTEM_ONE_BODY = {
  model: 'jev-latest',
  state:
    'A queue drops messages when the consumer is slower than the producer.',
  questions: {
    verdict: {
      type: 'choice',
      instructions: 'Is the queue lossy?',
      criteria: {
        lossy: 'Messages can be lost.',
        reliable: 'No message is lost.',
      },
    },
    confidence: {
      type: 'score',
      instructions: 'How sure are you about the verdict?',
      criteria: ['unsure', 'fairly sure', 'certain'],
    },
  },
}

/**
 * The request document the System One contract accepts. The local validator
 * (`relay/systemone_validate.go`) requires `model`, `state` and a non-empty
 * `questions` object, and each question's `type` selects the criteria shape,
 * so the example shows two of them rather than a single message string.
 */
export function SystemOneBodyExample() {
  const { t } = useTranslation()

  return (
    <div className='flex min-w-0 flex-col gap-3'>
      <DocsCode
        code={JSON.stringify(SYSTEM_ONE_BODY, null, 2)}
        title={t('apiDocs.systemone.bodyTitle', {
          defaultValue: 'Request body',
        })}
        copyLabel={t('apiDocs.systemone.copyBody', {
          defaultValue: 'Copy request body',
        })}
      />
      <ul className='text-muted-foreground flex list-disc flex-col gap-1 ps-5 text-sm'>
        <li>
          {t('apiDocs.systemone.fieldModel', {
            defaultValue: 'model: a jev-* decision model.',
          })}
        </li>{' '}
        <li>
          {t('apiDocs.systemone.fieldState', {
            defaultValue:
              'state: the text, object or array the questions are about.',
          })}
        </li>
        <li>
          {t('apiDocs.systemone.fieldQuestions', {
            defaultValue:
              'questions: a non-empty object; each entry has a type of noul, choice, score or box.',
          })}
        </li>
      </ul>
    </div>
  )
}
