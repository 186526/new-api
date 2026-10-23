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
import {
  createMemoryHistory,
  createRootRouteWithContext,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from '@tanstack/react-router'
import { render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

let refreshStatus = 401
let initialized = true
let setupStatus = 200
let restoreAdapter: (() => void) | undefined
const bundle = {
  access_token: 'test-access',
  token_type: 'Bearer',
  access_expires_at: 4_000_000_000,
  user: { id: 42, username: 'test-user', role: 1 },
  session: {
    sid: 'test-session',
    current: true,
    login_method: 'password',
    ip: '127.0.0.1',
    user_agent: 'test',
    created_at: 100,
    last_active_at: 100,
    expires_at: 4_000_000_000,
  },
}

beforeEach(() => {
  vi.resetModules()
  refreshStatus = 401
  initialized = true
  setupStatus = 200
  localStorage.clear()
  sessionStorage.clear()
  document.cookie = 'new-api-session=; Max-Age=0; Path=/'
})

afterEach(() => {
  restoreAdapter?.()
  restoreAdapter = undefined
  document.cookie = 'new_api_has_session=; Max-Age=0; Path=/'
  vi.restoreAllMocks()
})

async function createSiteRouter(href: string) {
  const { default: axios, AxiosError } = await import('axios')
  const originalAdapter = axios.defaults.adapter
  restoreAdapter = () => {
    axios.defaults.adapter = originalAdapter
  }
  axios.defaults.adapter = async (config) => {
    const isSetup = config.url === '/api/setup'
    if (!isSetup && config.url !== '/api/user/auth/refresh') {
      throw new Error(`Unexpected request: ${config.url}`)
    }
    const status = isSetup ? setupStatus : refreshStatus
    const response = {
      status,
      statusText: '',
      headers: {},
      config,
      data: isSetup
        ? { success: true, data: { status: initialized } }
        : { success: refreshStatus === 200, data: bundle },
    }
    if (status >= 400) {
      throw new AxiosError(
        'Request failed',
        undefined,
        config,
        undefined,
        response
      )
    }
    return response
  }
  const { Route } = await import('../__root')
  const { useAuthStore } = await import('@/stores/auth-store')
  useAuthStore.getState().auth.reset('idle')
  const root = createRootRouteWithContext<{ queryClient: QueryClient }>()({
    beforeLoad: Route.options.beforeLoad,
    component: Outlet,
    errorComponent: Route.options.errorComponent,
  })
  const businessLoader = vi.fn()
  const pages = [
    '/',
    '/pricing',
    '/about',
    '/dashboard',
    '/sign-up',
    '/register',
    '/sign-in',
    '/otp',
    '/oauth',
    '/forgot-password',
    '/reset',
    '/user/reset',
    '/setup',
  ]
  const children = pages.map((path) =>
    createRoute({
      getParentRoute: () => root,
      path,
      loader: path === '/sign-in' ? undefined : businessLoader,
    })
  )
  children.push(
    createRoute({ getParentRoute: () => root, path: '/oauth/$provider' })
  )
  const router = createRouter({
    routeTree: root.addChildren(children),
    history: createMemoryHistory({ initialEntries: [href] }),
    context: { queryClient: new QueryClient() },
  })
  return { router, businessLoader, useAuthStore }
}

test('anonymous direct visits redirect before loading business pages and retain query and hash', async () => {
  const { router, businessLoader } = await createSiteRouter(
    '/?view=usage#details'
  )
  await router.load()
  expect(router.state.location.pathname).toBe('/sign-in')
  expect(router.state.location.search.redirect).toBe('/?view=usage#details')
  expect(businessLoader).not.toHaveBeenCalled()
})

test('setup is public only when the server confirms initialization is incomplete', async () => {
  initialized = false
  const { router, businessLoader } = await createSiteRouter('/setup')
  await router.load()
  expect(router.state.location.pathname).toBe('/setup')
  expect(businessLoader).toHaveBeenCalledOnce()
})

test.each(['/sign-up', '/register'])(
  'registration %s redirects to sign-in without returning to registration',
  async (href) => {
    const { router } = await createSiteRouter(`${href}?aff=partner#form`)
    await router.load()
    expect(router.state.location.pathname).toBe('/sign-in')
    expect(router.state.location.search).toEqual({ aff: 'partner' })
    expect(router.state.location.hash).toBe('form')
  }
)

test.each([
  '/sign-in',
  '/otp',
  '/oauth',
  '/oauth/github',
  '/oauth/custom-provider/',
  '/forgot-password',
  '/reset',
  '/user/reset',
])('anonymous authentication entry %s remains reachable', async (href) => {
  const { router } = await createSiteRouter(`${href}?code=callback#continue`)
  await router.load()
  expect(router.state.location.href).toBe(`${href}?code=callback#continue`)
  expect(
    router.state.matches.every((match) => match.status === 'success')
  ).toBe(true)
})

test.each([
  '/pricing',
  '/about',
  '/dashboard',
  '/missing',
  '/sign-in/extra',
  '/otp/extra',
  '/oauth/github/extra',
  '/oauth-extra',
  '/reset/extra',
  '/user/reset/extra',
  '/setup-extra',
  '/setup/extra',
])(
  'anonymous business or unknown route %s redirects to login',
  async (href) => {
    const { router, businessLoader } = await createSiteRouter(href)
    await router.load()
    expect(router.state.location.pathname).toBe('/sign-in')
    expect(router.state.location.search.redirect).toBe(href)
    expect(businessLoader).not.toHaveBeenCalled()
  }
)

test.each([429, 503])(
  'refresh HTTP %s displays the existing route error without anonymous redirect',
  async (status) => {
    refreshStatus = status
    const { router, businessLoader } = await createSiteRouter('/pricing')
    await router.load()
    expect(router.state.location.pathname).toBe('/pricing')
    expect(router.state.matches[0].status).toBe('error')
    expect(router.state.matches[0].error).toMatchObject({
      response: { status },
    })
    expect(businessLoader).not.toHaveBeenCalled()
    expect(router.state.matches[0].error).not.toHaveProperty('config')
    expect(router.state.matches[0].error).not.toHaveProperty('cause')
    const consoleError = vi
      .spyOn(console, 'error')
      .mockImplementation(() => undefined)
    render(<RouterProvider router={router} />)
    await waitFor(() =>
      expect(
        screen.getByRole('heading', { name: String(status) })
      ).toBeVisible()
    )
    consoleError.mockRestore()
  }
)

test('invalid refresh response cannot load a business page', async () => {
  refreshStatus = 409
  const { router, businessLoader } = await createSiteRouter('/about')
  await router.load()
  expect(router.state.location.pathname).toBe('/sign-in')
  expect(businessLoader).not.toHaveBeenCalled()
})

test('initialized setup redirects through the authentication guard', async () => {
  const { router, businessLoader } = await createSiteRouter('/setup')
  await router.load()
  expect(router.state.location.pathname).toBe('/sign-in')
  expect(businessLoader).not.toHaveBeenCalled()
})

test('unavailable initialization status cannot expose setup', async () => {
  setupStatus = 503
  const { router, businessLoader } = await createSiteRouter('/setup')
  await router.load()
  expect(router.state.matches[0].status).toBe('error')
  expect(businessLoader).not.toHaveBeenCalled()
})

test('legacy conversion preserves query and hash before login return', async () => {
  const { router } = await createSiteRouter('/console/token?group=default#key')
  await router.load()
  expect(router.state.location.pathname).toBe('/sign-in')
  expect(router.state.location.search.redirect).toBe('/keys?group=default#key')
})

test('hintless refresh restores authentication and subsequent navigation stays usable', async () => {
  refreshStatus = 200
  const { router, businessLoader } = await createSiteRouter('/pricing')
  await router.load()
  expect(router.state.location.pathname).toBe('/pricing')
  expect(businessLoader).toHaveBeenCalledOnce()
  await router.navigate({ to: '/about' })
  expect(router.state.location.pathname).toBe('/about')
  expect(businessLoader).toHaveBeenCalledTimes(2)
})

test('SPA navigation after sign-out cannot load business pages', async () => {
  refreshStatus = 200
  const { router, businessLoader } = await createSiteRouter('/pricing')
  await router.load()
  const { clearAuthentication } = await import('@/lib/auth-session')
  clearAuthentication(false)
  refreshStatus = 401
  businessLoader.mockClear()
  await router.navigate({ to: '/sign-in' })
  await router.navigate({ to: '/about' })
  expect(router.state.location.pathname).toBe('/sign-in')
  expect(businessLoader).not.toHaveBeenCalled()
})
