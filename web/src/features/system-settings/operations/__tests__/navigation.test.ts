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
import { t } from 'i18next'
import { expect, it } from 'vitest'

import { getOperationsSectionNavItems } from '../section-registry'

it('keeps log maintenance and performance in Operations without the update page', () => {
  const urls = getOperationsSectionNavItems(t).map((item) => item.url)
  expect(urls).toContain('/system-settings/operations/logs')
  expect(urls).toContain('/system-settings/operations/performance')
  expect(urls).not.toContain('/system-settings/operations/update-checker')
})
