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

import { TOOLS_FETCH_MODEL, TOOLS_SEARCH_MODEL } from '../constants'

export function ChannelToolBadges({ models }: { models: string[] }) {
  const { t } = useTranslation()
  return (
    <>
      {models.includes(TOOLS_SEARCH_MODEL) && (
        <Badge variant='secondary'>{t('Search enabled')}</Badge>
      )}
      {models.includes(TOOLS_FETCH_MODEL) && (
        <Badge variant='secondary'>{t('Fetch enabled')}</Badge>
      )}
    </>
  )
}
