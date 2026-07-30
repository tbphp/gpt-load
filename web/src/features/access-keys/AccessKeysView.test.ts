import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'
import { nextTick } from 'vue'
import { createMemoryHistory } from 'vue-router'

import type { ApiClient, ApiRequestOptions } from '@/api/client'
import type { AccessKeyCreateResultDto, AccessKeyDto, GroupSummary } from '@/api/control/types'
import { NetworkError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import { createAppRouter } from '@/app/router'
import { mountApp } from '@/test/mount-app'

import AccessKeyCollection from './AccessKeyCollection.vue'
import AccessKeysView from './AccessKeysView.vue'

const canary = 'sk-gl-ACCESS_KEYS_LIST_CANARY'
const groups: GroupSummary[] = [
  {
    id: 7,
    name: 'Primary',
    upstream_url: 'https://api.example.com',
    protocols: ['openai-chat-completions'],
    models: [{ id: 'gpt-4.1', alias: 'public-gpt' }],
    enabled: true,
    key_count: 1,
  },
]
const keys: AccessKeyDto[] = [
  {
    id: 9,
    name: 'client',
    masked_key: 'sk-gl-••••••••cafe',
    status: 'active',
    filters: { groups: [], protocols: [], models: [] },
    rpm_limit: 0,
    created_at: '2026-07-28T00:00:00Z',
    updated_at: '2026-07-28T00:00:00Z',
  },
]

function queryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function serializeStorage(storage: Storage): string {
  return JSON.stringify(
    Array.from({ length: storage.length }, (_, index) => {
      const key = storage.key(index)
      return [key, key === null ? null : storage.getItem(key)]
    }),
  )
}

async function mountView(request: ApiClient['request']) {
  const client = queryClient()
  const mounted = await mountApp(AccessKeysView, {
    api: { request },
    queryClient: client,
    path: '/access-keys',
    locale: 'en-US',
    mounting: { attachTo: document.body },
  })
  await flushPromises()
  return { ...mounted, queryClient: client }
}

function documentButton(selector: string): HTMLButtonElement {
  const button = document.querySelector<HTMLButtonElement>(selector)
  if (!button) throw new Error(`missing ${selector}`)
  return button
}

describe('AccessKeysView', () => {
  it('replaces the route placeholder, loads real Groups and AccessKeys, and renders empty filters as all', async () => {
    const router = createAppRouter({ hasCredential: () => true }, createMemoryHistory())
    const routeComponent = router.resolve('/access-keys').matched.at(-1)?.components?.default
    expect(typeof routeComponent).toBe('function')
    expect(await (routeComponent as () => Promise<unknown>)()).toBe(AccessKeysView)

    const request = vi.fn(async (path: string, options?: ApiRequestOptions) => {
      if (path === '/api/access-keys' && options?.method === 'GET') return keys
      if (path === '/api/groups' && options?.method === 'GET') return groups
      if (path === '/api/access-keys/9/reveal' && options?.method === 'POST') {
        return { id: 9, key: canary, revealed_at: '2026-07-28T01:00:00Z' }
      }
      throw new Error(`unexpected ${path}`)
    }) as ApiClient['request']
    const { wrapper } = await mountView(request)

    expect(wrapper.get('[data-test="access-key-row-9"]').text()).toContain('All Groups')
    expect(wrapper.get('[data-test="access-key-row-9"]').text()).toContain('All protocols')
    expect(wrapper.get('[data-test="access-key-row-9"]').text()).toContain('All models')
    expect(wrapper.get('[data-test="access-key-row-9"]').text()).toContain('Unlimited')
    expect(wrapper.text()).not.toContain(canary)
    wrapper.unmount()
  })

  it('masks by default, reveals and copies only locally, then removes gcTime-zero plaintext on unmount', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    sessionStorage.setItem('arbitrary.session.slot', 'safe-session-value')
    localStorage.setItem('arbitrary.local.slot', 'safe-local-value')
    const request = vi.fn(async (path: string, options?: ApiRequestOptions) => {
      if (path === '/api/access-keys' && options?.method === 'GET') return keys
      if (path === '/api/groups' && options?.method === 'GET') return groups
      if (path === '/api/access-keys/9/reveal' && options?.method === 'POST') {
        return { id: 9, key: canary, revealed_at: '2026-07-28T01:00:00Z' }
      }
      throw new Error(`unexpected ${path}`)
    }) as ApiClient['request']
    const { queryClient: client, router, wrapper } = await mountView(request)

    expect(wrapper.text()).not.toContain(canary)
    await wrapper.get('[data-test="access-key-reveal-9"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain(canary)
    await wrapper.get('[data-test="access-key-copy-9"] button').trigger('click')
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith(canary)

    expect(JSON.stringify(router.currentRoute.value)).not.toContain(canary)
    expect(JSON.stringify(controlQueryKeys)).not.toContain(canary)
    expect(serializeStorage(sessionStorage)).not.toContain(canary)
    expect(serializeStorage(localStorage)).not.toContain(canary)
    expect(client.getMutationCache().getAll()).toHaveLength(0)

    wrapper.unmount()
    await flushPromises()
    expect(client.getQueryData(controlQueryKeys.accessKeys.list())).toBeUndefined()
    expect(JSON.stringify(client.getQueryCache().getAll())).not.toContain(canary)
    expect(document.body.textContent).not.toContain(canary)
    const snapshotSafeOutput = JSON.stringify({
      render: document.body.innerHTML,
      emitted: wrapper.emitted(),
    })
    expect(snapshotSafeOutput).not.toContain(canary)
  })

  it('conceals revealed plaintext when an AccessKey drawer closes', async () => {
    const request = vi.fn(async (path: string, options?: ApiRequestOptions) => {
      if (path === '/api/access-keys' && options?.method === 'GET') return keys
      if (path === '/api/groups' && options?.method === 'GET') return groups
      if (path === '/api/access-keys/9/reveal' && options?.method === 'POST') {
        return { id: 9, key: canary, revealed_at: '2026-07-28T01:00:00Z' }
      }
      throw new Error(`unexpected ${path}`)
    }) as ApiClient['request']
    const { wrapper } = await mountView(request)

    await wrapper.get('[data-test="access-key-reveal-9"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain(canary)

    await wrapper.get('[data-test="access-key-edit-9"]').trigger('click')
    await flushPromises()
    await vi.waitFor(() => expect(document.querySelector('.app-drawer__close')).not.toBeNull())
    documentButton('.app-drawer__close').click()
    await flushPromises()

    expect(wrapper.text()).not.toContain(canary)
    expect(wrapper.get('[data-test="access-key-reveal-9"]').attributes('aria-pressed')).toBe(
      'false',
    )
    wrapper.unmount()
  })

  it('retains masked stale list data and never renders generic error details', async () => {
    let listCalls = 0
    const request = vi.fn(async (path: string, options?: ApiRequestOptions) => {
      if (path === '/api/groups' && options?.method === 'GET') return groups
      if (path === '/api/access-keys' && options?.method === 'GET') {
        listCalls += 1
        if (listCalls === 1) return keys
        throw new Error('sk-gl-GENERIC_ERROR_CANARY')
      }
      throw new Error(`unexpected ${path}`)
    }) as ApiClient['request']
    const { queryClient: client, wrapper } = await mountView(request)

    await client.invalidateQueries({ queryKey: controlQueryKeys.accessKeys.list() })
    await flushPromises()
    expect(wrapper.get('[data-test="access-key-row-9"]')).toBeDefined()
    expect(wrapper.text()).toContain('may be stale')
    expect(wrapper.html()).not.toContain('sk-gl-GENERIC_ERROR_CANARY')
    expect(wrapper.html()).not.toContain(canary)
    wrapper.unmount()
  })

  it('focuses Create after deleting the only row and rendering the refreshed empty list', async () => {
    let currentKeys = [...keys]
    const request = vi.fn(async (path: string, options?: ApiRequestOptions) => {
      if (path === '/api/groups' && options?.method === 'GET') return groups
      if (path === '/api/access-keys' && options?.method === 'GET') return currentKeys
      if (path === '/api/access-keys/9' && options?.method === 'DELETE') {
        currentKeys = []
        return undefined
      }
      throw new Error(`unexpected ${path}`)
    }) as ApiClient['request']
    const { wrapper } = await mountView(request)

    await wrapper
      .get('[data-test="access-key-row-9"]')
      .get('[data-test="access-key-delete-open"]')
      .trigger('click')
    await flushPromises()
    documentButton('[data-test="access-key-delete-confirm"]').click()
    await flushPromises()

    expect(wrapper.find('[data-test="access-key-row-9"]').exists()).toBe(false)
    expect(document.activeElement).toBe(
      wrapper.get('button[data-test="access-key-create"]').element,
    )
    const announcement = wrapper.get('[data-test="access-key-delete-announcement"]')
    expect(announcement.attributes('aria-live')).toBe('polite')
    expect(announcement.text()).toContain('Deleted AccessKey “client”.')
    wrapper.unmount()
  })

  it('focuses Create after deleting one row and rendering the refreshed remaining rows', async () => {
    const otherKey: AccessKeyDto = {
      ...keys[0],
      id: 10,
      name: 'secondary',
      masked_key: 'sk-gl-••••••••beef',
    }
    let currentKeys = [...keys, otherKey]
    const request = vi.fn(async (path: string, options?: ApiRequestOptions) => {
      if (path === '/api/groups' && options?.method === 'GET') return groups
      if (path === '/api/access-keys' && options?.method === 'GET') return currentKeys
      if (path === '/api/access-keys/9' && options?.method === 'DELETE') {
        currentKeys = currentKeys.filter((accessKey) => accessKey.id !== 9)
        return undefined
      }
      throw new Error(`unexpected ${path}`)
    }) as ApiClient['request']
    const { wrapper } = await mountView(request)

    await wrapper
      .get('[data-test="access-key-row-9"]')
      .get('[data-test="access-key-delete-open"]')
      .trigger('click')
    await flushPromises()
    documentButton('[data-test="access-key-delete-confirm"]').click()
    await flushPromises()

    expect(wrapper.find('[data-test="access-key-row-9"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="access-key-row-10"]').exists()).toBe(true)
    expect(document.activeElement).toBe(
      wrapper.get('button[data-test="access-key-create"]').element,
    )
    wrapper.unmount()
  })

  it('keeps an unknown create operation at page scope and reconciles with the same identity', async () => {
    const created: AccessKeyCreateResultDto = {
      id: 11,
      name: 'reconcile-client',
      masked_key: 'sk-gl-••••••••babe',
      replayed: true,
      status: 'active',
      filters: { groups: [], protocols: [], models: [] },
      rpm_limit: 0,
      created_at: '2026-07-28T00:00:00Z',
      updated_at: '2026-07-28T00:00:00Z',
    }
    let createAttempts = 0
    const requestMock = vi.fn(async (path: string, options?: ApiRequestOptions) => {
      if (path === '/api/access-keys' && options?.method === 'GET') return keys
      if (path === '/api/groups' && options?.method === 'GET') return groups
      if (path === '/api/access-keys' && options?.method === 'POST') {
        createAttempts += 1
        if (createAttempts === 1) throw new NetworkError()
        return created
      }
      throw new Error(`unexpected ${path}`)
    })
    const request = requestMock as ApiClient['request']
    const { wrapper } = await mountView(request)

    expect(wrapper.find('[data-test="async-surface-loading"]').exists()).toBe(false)
    await wrapper.get('[data-test="access-key-create"]').trigger('click')
    await vi.waitFor(() =>
      expect(document.querySelector('[data-test="access-key-name"]')).not.toBeNull(),
    )
    const name = document.querySelector<HTMLInputElement>('[data-test="access-key-name"]')
    if (!name) throw new Error('missing create name')
    name.value = 'reconcile-client'
    name.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    documentButton('[data-test="access-key-save"]').click()
    await flushPromises()
    document
      .querySelector<HTMLButtonElement>('.access-key-drawer__actions .app-button--secondary')
      ?.click()
    await flushPromises()

    expect(wrapper.get('[data-test="access-key-operation-notice"]').text()).toContain(
      'outcome is unknown',
    )
    await wrapper.get('[data-test="access-key-operation-check"]').trigger('click')
    await flushPromises()
    documentButton('[data-test="access-key-save"]').click()
    await flushPromises()

    const createCalls = requestMock.mock.calls.filter(
      ([path, options]) => path === '/api/access-keys' && options?.method === 'POST',
    )
    expect(createCalls).toHaveLength(2)
    expect(new Headers(createCalls[1]?.[1]?.headers).get('Idempotency-Key')).toBe(
      new Headers(createCalls[0]?.[1]?.headers).get('Idempotency-Key'),
    )
    expect(createCalls[1]?.[1]?.json).toEqual(createCalls[0]?.[1]?.json)
    expect(wrapper.find('[data-test="access-key-operation-notice"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('keeps an unknown edit operation at page scope after the drawer closes', async () => {
    const applied: AccessKeyDto = { ...keys[0], name: 'renamed-client' }
    const secondary: AccessKeyDto = {
      ...keys[0],
      id: 10,
      name: 'secondary-client',
      masked_key: 'sk-gl-••••••••beef',
    }
    let listCalls = 0
    const requestMock = vi.fn(async (path: string, options?: ApiRequestOptions) => {
      if (path === '/api/groups' && options?.method === 'GET') return groups
      if (path === '/api/access-keys' && options?.method === 'GET') {
        listCalls += 1
        return listCalls === 1 ? [keys[0], secondary] : [applied, secondary]
      }
      if (path === '/api/access-keys/9' && options?.method === 'PUT') throw new NetworkError()
      throw new Error(`unexpected ${path}`)
    })
    const confirm = vi.fn(() => false)
    vi.stubGlobal('confirm', confirm)
    const { wrapper } = await mountView(requestMock as ApiClient['request'])

    await wrapper.get('[data-test="access-key-edit-9"]').trigger('click')
    await flushPromises()
    await vi.waitFor(() =>
      expect(document.querySelector('[data-test="access-key-name"]')).not.toBeNull(),
    )
    const name = document.querySelector<HTMLInputElement>('[data-test="access-key-name"]')
    if (!name) throw new Error('missing edit name')
    name.value = 'renamed-client'
    name.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    documentButton('[data-test="access-key-save"]').click()
    await flushPromises()

    const close = documentButton('.app-drawer__close')
    expect(close.disabled).toBe(false)
    close.click()
    await flushPromises()

    expect(confirm).not.toHaveBeenCalled()
    expect(document.querySelector('[role="dialog"]')).toBeNull()
    expect(wrapper.get('[data-test="access-key-edit-operation-notice"]').text()).toContain(
      'renamed-client',
    )
    expect(wrapper.get('[data-test="access-key-edit-operation-notice"]').text()).toContain(
      'unknown outcome',
    )

    await wrapper.get('[data-test="access-key-edit-10"]').trigger('click')
    await flushPromises()
    expect(document.querySelector<HTMLInputElement>('[data-test="access-key-name"]')?.value).toBe(
      'renamed-client',
    )
    documentButton('.app-drawer__close').click()
    await flushPromises()

    await wrapper.get('[data-test="access-key-edit-operation-check"]').trigger('click')
    await flushPromises()
    expect(document.querySelector('[role="dialog"]')).not.toBeNull()
    documentButton('[data-test="access-key-save"]').click()
    await flushPromises()

    expect(
      requestMock.mock.calls.filter(
        ([path, options]) => path === '/api/access-keys/9' && options?.method === 'PUT',
      ),
    ).toHaveLength(1)
    expect(listCalls).toBe(2)
    expect(wrapper.find('[data-test="access-key-edit-operation-notice"]').exists()).toBe(false)
    wrapper.unmount()
    vi.unstubAllGlobals()
  })

  it('does not focus detached content when deleted is followed by immediate unmount', async () => {
    const request = vi.fn(async (path: string, options?: ApiRequestOptions) => {
      if (path === '/api/access-keys' && options?.method === 'GET') return keys
      if (path === '/api/groups' && options?.method === 'GET') return groups
      throw new Error(`unexpected ${path}`)
    }) as ApiClient['request']
    const { wrapper } = await mountView(request)
    const createButton = wrapper.get<HTMLButtonElement>(
      'button[data-test="access-key-create"]',
    ).element
    const focus = vi.spyOn(createButton, 'focus')

    wrapper.getComponent(AccessKeyCollection).vm.$emit('deleted', 'client')
    wrapper.unmount()
    await nextTick()
    await flushPromises()

    expect(createButton.isConnected).toBe(false)
    expect(focus).not.toHaveBeenCalled()
  })
})
