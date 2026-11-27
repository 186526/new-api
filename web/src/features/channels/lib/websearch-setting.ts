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
import {
  CHANNEL_TYPE_ADVANCED_CUSTOM,
  CHANNEL_TYPE_ANTIGRAVITY,
} from '../constants'

/** DeepSeek, the second channel type whose upstream sells search and fetch. */
const CHANNEL_TYPE_DEEPSEEK = 43

/**
 * Channel types whose upstream can answer a search or a fetch. The backend
 * derives the provider from the type, and every other type answers "this
 * channel does not support this endpoint", so the settings are shown only here.
 */
const WEB_SEARCH_CHANNEL_TYPES: ReadonlySet<number> = new Set([
  CHANNEL_TYPE_ANTIGRAVITY,
  CHANNEL_TYPE_DEEPSEEK,
  // An advanced custom channel reaches whatever upstream its base URL names,
  // so an Exa key can be bound to one here instead of globally.
  CHANNEL_TYPE_ADVANCED_CUSTOM,
])

export function supportsWebSearchSettings(channelType: number): boolean {
  return WEB_SEARCH_CHANNEL_TYPES.has(channelType)
}
