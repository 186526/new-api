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
import type { TopNavLink } from '../types'

/**
 * Default top navigation links
 *
 * In practice, navigation links are dynamically fetched from backend.
 * Priority: Backend dynamic links > Provided navLinks > defaultTopNavLinks
 *
 * Only entries that always resolve inside this deployment belong here. `Docs`
 * points at the gateway's own `/docs` page, never at an external site; an
 * administrator who wants an external documentation link sets `docs_link` in
 * the header navigation settings, which takes precedence.
 */
export const defaultTopNavLinks: TopNavLink[] = [
  { title: 'Docs', href: '/docs' },
]
