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

/**
 * The persisted `/api/status` snapshot, and the one rule about its lifetime.
 *
 * The snapshot lets a cold start render the site name and favicon before the
 * network answers, so it outlives the tab that wrote it. Most of its fields
 * describe the site and are the same for every caller, but a few describe the
 * caller: the backend answers them only to a signed-in session and marks that
 * whole response `no-store, private`. Persisting them past the session would
 * hand the next visitor on the same browser — another account, or nobody —
 * configuration that was never theirs to read.
 *
 * This module holds only the storage mechanics, so `lib/auth-session` (which
 * knows when a session ends) can drop those fields without importing
 * `lib/status-query`, which reaches it back through `lib/api`.
 */

export const STATUS_STORAGE_KEY = 'status'

/**
 * Status fields that are only true for the session that read them.
 *
 * `SidebarModulesAdmin` is the admin sidebar switch board. `controller/misc.go`
 * writes it only when the caller is signed in, and the sidebar gates read it
 * from this snapshot, so a stale copy is not just stale data: it decides which
 * admin entries render.
 */
const CREDENTIAL_BOUND_STATUS_FIELDS = ['SidebarModulesAdmin'] as const

export type StatusSnapshot = Record<string, unknown>

/** Read the last persisted snapshot; `null` when absent or unreadable. */
export function readStatusSnapshot(): StatusSnapshot | null {
  try {
    if (typeof window === 'undefined') return null
    const raw = window.localStorage.getItem(STATUS_STORAGE_KEY)
    return raw ? (JSON.parse(raw) as StatusSnapshot) : null
  } catch {
    return null
  }
}

export function writeStatusSnapshot(status: StatusSnapshot): void {
  try {
    if (typeof window !== 'undefined') {
      window.localStorage.setItem(STATUS_STORAGE_KEY, JSON.stringify(status))
    }
  } catch {
    /* Storage can be unavailable in private mode. */
  }
}

/**
 * Drop the fields that belonged to a session which has ended.
 *
 * The site-wide fields stay: they are what makes a signed-out cold start render
 * the configured name instead of the built-in default.
 */
export function forgetSignedInStatus(): void {
  const status = readStatusSnapshot()
  if (!status) return

  const remaining = { ...status }
  for (const field of CREDENTIAL_BOUND_STATUS_FIELDS) {
    delete remaining[field]
  }
  writeStatusSnapshot(remaining)
}
