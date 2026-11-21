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
import { QueryClient } from '@tanstack/react-query'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

/**
 * Guard the boot of a reload by a signed-in visitor who keeps the anonymous
 * `/api/status` read in flight until after the Refresh Cookie is exchanged.
 *
 * `main.tsx` primes `['status']` at module evaluation, when memory holds no
 * credential, so that read goes out anonymous. The root route then resolves the
 * Refresh Cookie in `beforeLoad`, while the anonymous read is still pending.
 * `/api/status` returns `SidebarModulesAdmin` only to a signed-in caller, and
 * the sidebar gates read the snapshot it persists — so the dropped anonymous
 * read must neither fill the cache nor overwrite that snapshot.
 */

type AxiosRequest = import('axios').InternalAxiosRequestConfig
type Backend = (config: AxiosRequest) => Promise<unknown>

// The adapter seam. `@/lib/http-client` creates its own axios instance from
// its own copy of the axios module, so the mock intercepts `create` and gives
// every instance an adapter that delegates to `backend`. Both clients the boot
// path uses — the refresh exchange and `api` — answer from it.
let backend: Backend = async () => {
  throw new Error('no backend stub installed')
}

vi.mock('axios', async (orig) => {
  const actual = await orig<typeof import('axios')>()
  const create = actual.default.create.bind(actual.default)
  const adapter = ((config: never) => backend(config as never)) as never
  const withAdapter = (config?: import('axios').AxiosRequestConfig) => {
    const instance = create(config)
    instance.defaults.adapter = adapter
    return instance
  }
  // Instances built after this point get it through `create`; assigning the
  // module default too covers a client built through `axios(...)` directly.
  actual.default.defaults.adapter = adapter
  const mock = Object.assign(
    (config: import('axios').AxiosRequestConfig) =>
      actual.default.request({ ...config, adapter }),
    actual.default,
    { create: withAdapter }
  )
  return { ...actual, default: mock }
})

const adminSidebarConfig = JSON.stringify({
  chat: { enabled: true, playground: false, chat: true },
})

const bundle = {
  access_token: 'boot-access-token',
  token_type: 'Bearer',
  access_expires_at: 4_000_000_000,
  user: { id: 7, username: 'reloading-admin', role: 10 },
  session: {
    sid: 'restored-session',
    current: true,
    login_method: 'password',
    ip: '127.0.0.1',
    user_agent: 'test',
    created_at: 100,
    last_active_at: 100,
    expires_at: 4_000_000_000,
  },
}

type AuthModules = {
  useAuthStore: typeof import('@/stores/auth-store').useAuthStore
  resolveAuthentication: typeof import('@/lib/auth-session').resolveAuthentication
  clearAuthenticatedClientState: typeof import('@/lib/auth-session').clearAuthenticatedClientState
  isSidebarModuleEnabled: typeof import('@/lib/nav-modules').isSidebarModuleEnabled
}

let auth: AuthModules

/** Records every `/api/status` request, in the order the server received it. */
let statusRequests: { url: string; signedIn: boolean }[] = []

/** An axios-shaped rejection, which is what the refresh runner inspects. */
function axiosError(config: AxiosRequest, status: number): Error {
  return Object.assign(new Error('Request failed'), {
    isAxiosError: true,
    config,
    response: { status, statusText: '', headers: {}, config, data: {} },
  })
}

/**
 * `/api/user/auth/refresh` stands in for the Refresh Cookie exchange the root
 * route performs; `/api/status` answers with the variant the request's
 * Authorization header earned.
 *
 * `holdAnonymousStatus` keeps the first anonymous `/api/status` pending until
 * the returned release function runs, which is the ordering a hard reload
 * produces when the refresh exchange wins.
 */
function stubBackend(options: {
  refresh: { status: number }
  holdAnonymousStatus?: boolean
}): () => void {
  let releaseAnonymous = () => {}
  const anonymousGate = new Promise<void>((resolve) => {
    releaseAnonymous = resolve
  })

  backend = async (config) => {
    const response = { status: 200, statusText: 'OK', headers: {}, config }
    if (config.url === '/api/user/auth/refresh') {
      if (options.refresh.status >= 400) {
        throw axiosError(config, options.refresh.status)
      }
      return { ...response, data: { success: true, data: bundle } }
    }
    if (config.url === '/api/status') {
      const signedIn = Boolean(config.headers.get('Authorization'))
      statusRequests.push({ url: config.url, signedIn })
      if (!signedIn && options.holdAnonymousStatus) {
        await anonymousGate
      }
      return {
        ...response,
        data: {
          success: true,
          data: {
            system_name: signedIn ? 'signed in' : 'anonymous',
            ...(signedIn ? { SidebarModulesAdmin: adminSidebarConfig } : {}),
          },
        },
      }
    }
    throw new Error(`Unexpected request: ${config.url}`)
  }

  return releaseAnonymous
}

function createQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

/**
 * The boot sequence `main.tsx` performs: create the app-wide client, install
 * the session reset, then start the status read.
 *
 * The read is deliberately not awaited. `main.tsx` fires it without awaiting,
 * and the point of these tests is what happens while it is still in flight.
 */
async function bootApp(): Promise<{
  queryClient: QueryClient
  primed: Promise<unknown>
}> {
  const { subscribeSignedInCacheReset } = await import('@/lib/auth-session')
  const { statusQueryOptions } = await import('@/lib/status-query')
  const { reloadStatusAfterSessionChange } = await import('@/lib/status-query')
  const queryClient = createQueryClient()
  subscribeSignedInCacheReset(queryClient, reloadStatusAfterSessionChange)
  const primed = queryClient.ensureQueryData(statusQueryOptions).catch(() => {})
  return { queryClient, primed }
}

/** Let the pending `/api/status` reach the stub and block there. */
function waitForAnonymousRequest(): Promise<void> {
  return vi.waitFor(() => {
    expect(statusRequests.some((request) => !request.signedIn)).toBe(true)
  })
}

/**
 * `main.tsx` runs for its side effects and cannot be imported here, so assert on
 * its source that the reset is installed after the router exists and before the
 * first route resolves. Without this, deleting the call leaves the rest of the
 * suite green.
 */
test('main installs the session cache reset before the router resolves', async () => {
  const { default: source } = await import('../../main.tsx?raw')

  const routerCreated = source.indexOf('const router = createRouter(')
  const subscribed = source.indexOf('subscribeSignedInCacheReset(queryClient')
  const rendered = source.indexOf('root.render(')

  expect(routerCreated).toBeGreaterThan(-1)
  expect(subscribed).toBeGreaterThan(routerCreated)
  expect(subscribed).toBeLessThan(rendered)
  expect(source).toContain('import { subscribeSignedInCacheReset }')
})

beforeEach(async () => {
  window.localStorage.clear()
  document.cookie = ''
  statusRequests = []
  backend = async () => {
    throw new Error('no backend stub installed')
  }
  // A fresh module graph per test: the auth session, the reset subscription and
  // the status query all hold module state that a reload would have recreated.
  vi.resetModules()
  const { useAuthStore } = await import('@/stores/auth-store')
  const { resolveAuthentication, clearAuthenticatedClientState } = await import(
    '@/lib/auth-session'
  )
  const { isSidebarModuleEnabled } = await import('@/lib/nav-modules')
  useAuthStore.getState().auth.reset('idle')
  auth = {
    useAuthStore,
    resolveAuthentication,
    clearAuthenticatedClientState,
    isSidebarModuleEnabled,
  }
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.resetModules()
})

test('an anonymous status read still in flight at sign-in refills the cache with the signed-in variant', async () => {
  const releaseAnonymous = stubBackend({
    refresh: { status: 200 },
    holdAnonymousStatus: true,
  })

  // 1. Module evaluation: the anonymous read goes out and stays pending.
  const { queryClient, primed } = await bootApp()
  await waitForAnonymousRequest()

  // 2. The root route's beforeLoad exchanges the Refresh Cookie for a session
  //    while that read is still in flight.
  expect((await auth.resolveAuthentication()).kind).toBe('authenticated')

  // 3. The anonymous response arrives before the signed-in refill can land, the
  //    ordering a slow anonymous request produces. It is the read the reset
  //    dropped, so it must leave no trace — not in the cache, not in the
  //    snapshot the gates read.
  releaseAnonymous()
  await primed

  // 4. The session reset refills the cache with the signed-in variant, and that
  //    refill rewrites the snapshot. Reading it here, without a consumer asking
  //    for status, separates a refill from a later consumer fetching from an
  //    empty cache and masking a missing one.
  const { STATUS_QUERY_KEY, STATUS_STORAGE_KEY } = await import(
    '@/lib/status-query'
  )
  await vi.waitFor(() => {
    const cached = queryClient.getQueryData<Record<string, unknown>>(
      STATUS_QUERY_KEY
    )
    expect(cached?.SidebarModulesAdmin).toBe(adminSidebarConfig)
    // The gates read this snapshot, not the cache entry, so both have to be in
    // place before the assertions below mean anything.
    const persisted = window.localStorage.getItem(STATUS_STORAGE_KEY)
    expect(persisted).toContain('SidebarModulesAdmin')
  })

  expect(auth.isSidebarModuleEnabled('chat', 'playground')).toBe(false)
})

test('an anonymous response released after the signed-in refill cannot overwrite it', async () => {
  const releaseAnonymous = stubBackend({
    refresh: { status: 200 },
    holdAnonymousStatus: true,
  })

  const { queryClient, primed } = await bootApp()
  await waitForAnonymousRequest()
  expect((await auth.resolveAuthentication()).kind).toBe('authenticated')

  // The refill reaches the stub and lands while the anonymous request is still
  // held; only then is the anonymous response released. This is the slower
  // half of the same race — the dropped read resolving last — and the ordering
  // that makes a missing guard observable: the read still carries the anonymous
  // variant but its signal is already aborted.
  const { STATUS_QUERY_KEY, STATUS_STORAGE_KEY } = await import(
    '@/lib/status-query'
  )
  await vi.waitFor(() => {
    expect(
      queryClient.getQueryData<Record<string, unknown>>(STATUS_QUERY_KEY)
        ?.SidebarModulesAdmin
    ).toBe(adminSidebarConfig)
  })

  releaseAnonymous()
  await primed
  // Give the released response every turn it needs to try to write.
  await new Promise((resolve) => globalThis.setTimeout(resolve, 0))
  await new Promise((resolve) => globalThis.setTimeout(resolve, 0))

  const cached = queryClient.getQueryData<Record<string, unknown>>(
    STATUS_QUERY_KEY
  )
  expect(cached?.SidebarModulesAdmin).toBe(adminSidebarConfig)
  expect(cached?.system_name).toBe('signed in')
  expect(window.localStorage.getItem(STATUS_STORAGE_KEY)).toContain(
    'SidebarModulesAdmin'
  )
  expect(auth.isSidebarModuleEnabled('chat', 'playground')).toBe(false)
})

test('the signed-in refill carries the session credential, not the anonymous one', async () => {
  const releaseAnonymous = stubBackend({
    refresh: { status: 200 },
    holdAnonymousStatus: true,
  })
  const { primed } = await bootApp()
  await waitForAnonymousRequest()

  expect((await auth.resolveAuthentication()).kind).toBe('authenticated')
  releaseAnonymous()
  await primed
  await vi.waitFor(() => {
    expect(statusRequests.some((request) => request.signedIn)).toBe(true)
  })

  expect(statusRequests).toEqual([
    { url: '/api/status', signedIn: false },
    { url: '/api/status', signedIn: true },
  ])
})

test('without the reset subscription the primed anonymous entry survives', async () => {
  // The negative control: this is the regression the subscription prevents.
  stubBackend({ refresh: { status: 200 } })
  const { statusQueryOptions, ensureStatus } = await import(
    '@/lib/status-query'
  )
  const queryClient = createQueryClient()
  await queryClient.ensureQueryData(statusQueryOptions)
  expect((await auth.resolveAuthentication()).kind).toBe('authenticated')

  const status = await ensureStatus(queryClient)

  expect(status?.SidebarModulesAdmin).toBeUndefined()
  expect(auth.isSidebarModuleEnabled('chat', 'playground')).toBe(true)
})

test('signing out clears the cache without reading status for the closed session', async () => {
  stubBackend({ refresh: { status: 200 } })
  const { queryClient } = await bootApp()
  expect((await auth.resolveAuthentication()).kind).toBe('authenticated')
  await vi.waitFor(() => {
    expect(statusRequests.some((request) => request.signedIn)).toBe(true)
  })
  const requestsBeforeSignOut = statusRequests.length

  auth.clearAuthenticatedClientState(queryClient, false)

  const { STATUS_QUERY_KEY } = await import('@/lib/status-query')
  expect(queryClient.getQueryData(STATUS_QUERY_KEY)).toBeUndefined()
  expect(statusRequests.length).toBe(requestsBeforeSignOut)
})

test('signing out drops the sidebar config the signed-in variant persisted', async () => {
  stubBackend({ refresh: { status: 200 } })
  const { queryClient } = await bootApp()
  expect((await auth.resolveAuthentication()).kind).toBe('authenticated')

  const { STATUS_STORAGE_KEY } = await import('@/lib/status-query')
  await vi.waitFor(() => {
    expect(window.localStorage.getItem(STATUS_STORAGE_KEY)).toContain(
      'SidebarModulesAdmin'
    )
  })

  auth.clearAuthenticatedClientState(queryClient, false)

  // The snapshot outlives the tab, and `SidebarModulesAdmin` is the field the
  // backend only answers to a signed-in caller and marks `no-store, private`.
  // Left behind, the next visitor on this browser reads the previous admin's
  // sidebar switches as their own until their own status read lands.
  const persisted = JSON.parse(
    window.localStorage.getItem(STATUS_STORAGE_KEY) ?? '{}'
  ) as Record<string, unknown>
  expect(persisted).not.toHaveProperty('SidebarModulesAdmin')
  // Branding is the anonymous variant's too, so a signed-out cold start still
  // renders the site name instead of the built-in default.
  expect(persisted.system_name).toBe('signed in')
  expect(auth.isSidebarModuleEnabled('chat', 'playground')).toBe(true)
})

test('a different session replacing the old one drops the old sidebar config at once', async () => {
  stubBackend({ refresh: { status: 200 } })
  const { queryClient } = await bootApp()
  expect((await auth.resolveAuthentication()).kind).toBe('authenticated')

  const { STATUS_STORAGE_KEY } = await import('@/lib/status-query')
  await vi.waitFor(() => {
    expect(window.localStorage.getItem(STATUS_STORAGE_KEY)).toContain(
      'SidebarModulesAdmin'
    )
  })

  // A second account signing in without the first one signing out: the refill
  // for the new session has not landed yet, so only the removal protects the
  // gates in between.
  const { applyAuthBundle } = await import('@/lib/auth-session')
  applyAuthBundle({
    ...bundle,
    access_token: 'second-account-token',
    session: { ...bundle.session, sid: 'second-session' },
  })

  const persisted = JSON.parse(
    window.localStorage.getItem(STATUS_STORAGE_KEY) ?? '{}'
  ) as Record<string, unknown>
  expect(persisted).not.toHaveProperty('SidebarModulesAdmin')
  // The query cache keeps the old entry until its own reset path runs; assert
  // the snapshot contract only, which is what the gates read first.
  void queryClient
})

test('an anonymous boot keeps the anonymous variant', async () => {
  stubBackend({ refresh: { status: 401 } })
  const { queryClient, primed } = await bootApp()
  const { ensureStatus } = await import('@/lib/status-query')
  await primed

  expect((await auth.resolveAuthentication()).kind).toBe('anonymous')
  const status = await ensureStatus(queryClient)

  expect(status?.SidebarModulesAdmin).toBeUndefined()
})
