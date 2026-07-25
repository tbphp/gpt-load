import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import type { ApiClient, ApiRequestOptions } from '@/api/client'
import type { GroupDetailDto } from '@/api/control/groups'
import { ApiError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import { mountApp } from '@/test/mount-app'

import GroupDeleteDialog from './GroupDeleteDialog.vue'

const detail: GroupDetailDto = {
  id: 7,
  name: 'Primary',
  upstream_url: 'https://api.example.com/v1',
  protocols: ['openai'],
  models: [{ id: 'gpt-4o', alias: '' }],
  enabled: true,
  key_count: 1,
  validation_model: null,
  weight_manual: null,
  config: {},
  effective_config: {
    connect_timeout: 15,
    first_byte_timeout: 120,
    request_timeout: 600,
    stream_idle_timeout: 300,
    header_rules: { set: {}, remove: [] },
  },
}

function queryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

async function mountDelete(request: ApiClient['request']) {
  const client = queryClient()
  client.setQueryData(controlQueryKeys.groups.detail(7), detail)
  client.setQueryData(controlQueryKeys.groups.keys(7), [{ id: 31 }])
  const mounted = await mountApp(GroupDeleteDialog, {
    api: { request },
    queryClient: client,
    path: '/groups/7?tab=settings',
    locale: 'en-US',
    mounting: {
      props: { groupId: 7, groupName: 'Primary' },
      attachTo: document.body,
    },
  })
  return { ...mounted, queryClient: client }
}

function documentButton(selector: string): HTMLButtonElement {
  const element = document.querySelector<HTMLButtonElement>(selector)
  if (!element) throw new Error(`missing ${selector}`)
  return element
}

function documentInput(selector: string): HTMLInputElement {
  const element = document.querySelector<HTMLInputElement>(selector)
  if (!element) throw new Error(`missing ${selector}`)
  return element
}

describe('GroupDeleteDialog', () => {
  it('requires the exact typed Group name, then removes sensitive queries and replaces Home on success', async () => {
    const requestMock = vi.fn(async (path: string, options?: ApiRequestOptions) => {
      if (path === '/api/groups/7' && options?.method === 'DELETE') return undefined
      throw new Error(`unexpected request: ${path}`)
    })
    const {
      queryClient: client,
      router,
      wrapper,
    } = await mountDelete(requestMock as ApiClient['request'])
    const remove = vi.spyOn(client, 'removeQueries')
    const invalidate = vi.spyOn(client, 'invalidateQueries')

    await wrapper.get('[data-test="group-delete-open"]').trigger('click')
    await flushPromises()
    const confirm = documentButton('[data-test="group-delete-confirm"]')
    expect(confirm.disabled).toBe(true)

    const input = documentInput('[data-test="group-delete-name"]')
    expect(document.querySelector('[role="dialog"]')?.getAttribute('aria-labelledby')).toBeTruthy()
    expect(document.activeElement).toBe(input)
    input.value = 'primary'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    expect(confirm.disabled).toBe(true)

    input.value = 'Primary'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    expect(confirm.disabled).toBe(false)
    confirm.click()
    await flushPromises()

    expect(requestMock).toHaveBeenCalledWith('/api/groups/7', {
      method: 'DELETE',
      signal: expect.any(AbortSignal),
    })
    expect(remove.mock.calls.map(([filters]) => filters)).toEqual([
      { queryKey: controlQueryKeys.groups.detail(7), exact: true },
      { queryKey: controlQueryKeys.groups.keys(7), exact: true },
    ])
    expect(invalidate.mock.calls.map(([filters]) => filters)).toEqual([
      { queryKey: controlQueryKeys.groups.list() },
      { queryKey: controlQueryKeys.health() },
    ])
    expect(router.currentRoute.value.name).toBe('home')
    wrapper.unmount()
  })

  it('retains the page, cache, and typed form while rendering GROUP_IN_USE references', async () => {
    const request = vi.fn().mockRejectedValue(
      new ApiError(409, 'GROUP_IN_USE', 'must not render generic server detail', {
        access_keys: [
          { id: 11, name: 'Production clients' },
          { id: 19, name: 'Canary clients' },
        ],
      }),
    ) as ApiClient['request']
    const { queryClient: client, router, wrapper } = await mountDelete(request)
    const remove = vi.spyOn(client, 'removeQueries')
    const invalidate = vi.spyOn(client, 'invalidateQueries')

    await wrapper.get('[data-test="group-delete-open"]').trigger('click')
    await flushPromises()
    const input = documentInput('[data-test="group-delete-name"]')
    input.value = 'Primary'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    documentButton('[data-test="group-delete-confirm"]').click()
    await flushPromises()

    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('Production clients')
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('Canary clients')
    expect(document.querySelector('[role="dialog"]')?.textContent).not.toContain(
      'must not render generic server detail',
    )
    expect(documentInput('[data-test="group-delete-name"]').value).toBe('Primary')
    expect(client.getQueryData(controlQueryKeys.groups.detail(7))).toEqual(detail)
    expect(client.getQueryData(controlQueryKeys.groups.keys(7))).toEqual([{ id: 31 }])
    expect(remove).not.toHaveBeenCalled()
    expect(invalidate).not.toHaveBeenCalled()
    expect(router.currentRoute.value.name).toBe('group-detail')
    wrapper.unmount()
  })

  it('falls back to fixed generic feedback for malformed GROUP_IN_USE data', async () => {
    const request = vi
      .fn()
      .mockRejectedValue(
        new ApiError(409, 'GROUP_IN_USE', 'must not render', { access_keys: [] }),
      ) as ApiClient['request']
    const { wrapper } = await mountDelete(request)
    await wrapper.get('[data-test="group-delete-open"]').trigger('click')
    await flushPromises()
    const input = documentInput('[data-test="group-delete-name"]')
    input.value = 'Primary'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    documentButton('[data-test="group-delete-confirm"]').click()
    await flushPromises()

    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(
      'Unable to delete the Group.',
    )
    expect(document.querySelector('[role="dialog"]')?.textContent).not.toContain('must not render')
    wrapper.unmount()
  })
})
