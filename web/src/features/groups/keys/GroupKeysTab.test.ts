import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import type { ApiClient, ApiRequestOptions } from '@/api/client'
import { ApiError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import { mountApp } from '@/test/mount-app'

import GroupKeysTab from './GroupKeysTab.vue'

const keys = [
  {
    id: 11,
    group_id: 7,
    mask: 'sk-p****a1b2',
    status: 'active' as const,
    effective_status: 'available' as const,
    weight_manual: null,
    weight_auto: 72,
    blacklisted: false,
    cooldown_until: null,
    failure_count: 0,
  },
  {
    id: 12,
    group_id: 7,
    mask: 'sk-p****c3d4',
    status: 'disabled' as const,
    effective_status: 'disabled' as const,
    weight_manual: 50,
    weight_auto: 61,
    blacklisted: false,
    cooldown_until: null,
    failure_count: 3,
  },
]

function queryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

async function mountKeys(request: ApiClient['request']) {
  const client = queryClient()
  const mounted = await mountApp(GroupKeysTab, {
    api: { request },
    queryClient: client,
    path: '/groups/7?tab=keys',
    locale: 'en-US',
    mounting: { props: { groupId: 7 }, attachTo: document.body },
  })
  await flushPromises()
  return { ...mounted, queryClient: client }
}

function clickDocument(selector: string): void {
  const element = document.querySelector<HTMLButtonElement>(selector)
  if (!element) throw new Error(`missing element ${selector}`)
  element.click()
}

describe('GroupKeysTab', () => {
  it('renders only masks and provides icon, text, and semantic tone for configured and effective status', async () => {
    const plaintext = 'UPSTREAM_KEY_PLAINTEXT_CANARY_7f21'
    const request = vi.fn(async (path: string) => {
      if (path === '/api/groups/7/keys') return keys
      throw new ApiError(500, 'INTERNAL_SERVER_ERROR', plaintext)
    }) as ApiClient['request']
    const { wrapper } = await mountKeys(request)

    expect(wrapper.text()).toContain('sk-p****a1b2')
    expect(wrapper.text()).toContain('sk-p****c3d4')
    expect(wrapper.text()).not.toContain(plaintext)
    const row = wrapper.get('[data-test="key-row-11"]')
    expect(row.text()).toContain('Active')
    expect(row.text()).toContain('Available')
    expect(row.findAll('.status-badge--success')).toHaveLength(2)
    expect(row.findAll('.status-badge svg').length).toBeGreaterThanOrEqual(2)

    const weight = wrapper.get('[data-test="key-weight-11"]')
    const optionValues = weight.findAll('option').map((option) => option.attributes('value'))
    expect(optionValues).toContain('auto')
    expect(optionValues).toContain('1')
    expect(optionValues).toContain('100')
    expect(optionValues).not.toContain('0')
    expect(wrapper.get('[data-test="key-save-11"]').attributes()).toHaveProperty('disabled')
    wrapper.unmount()
  })

  it('renders a backend-valid existing zero weight without exposing zero for other keys', async () => {
    const zeroWeightKey = {
      ...keys[0],
      weight_manual: 0,
      effective_status: 'disabled' as const,
    }
    const request = vi.fn(async (path: string) => {
      if (path === '/api/groups/7/keys') return [zeroWeightKey]
      throw new Error(`unexpected request: ${path}`)
    }) as ApiClient['request']
    const { wrapper } = await mountKeys(request)

    expect(wrapper.get('[data-test="key-row-11"]')).toBeDefined()
    const weight = wrapper.get('[data-test="key-weight-11"]')
    expect((weight.element as HTMLSelectElement).value).toBe('0')
    expect(weight.findAll('option').map((option) => option.attributes('value'))).toEqual([
      'auto',
      '0',
      ...Array.from({ length: 100 }, (_, index) => String(index + 1)),
    ])
    expect(wrapper.get('[data-test="key-save-11"]').attributes()).toHaveProperty('disabled')
    wrapper.unmount()
  })

  it('updates with a changed-only body and invalidates exactly Group keys/detail/list plus health', async () => {
    const requestMock = vi.fn(async (path: string, options?: ApiRequestOptions) => {
      if (path === '/api/groups/7/keys' && options?.method === 'GET') return keys
      if (path === '/api/groups/7/keys/11' && options?.method === 'PUT') {
        return { ...keys[0], weight_manual: 50 }
      }
      throw new Error(`unexpected request: ${path}`)
    })
    const { queryClient: client, wrapper } = await mountKeys(requestMock as ApiClient['request'])
    const invalidate = vi.spyOn(client, 'invalidateQueries')

    await wrapper.get('[data-test="key-weight-11"]').setValue('50')
    expect(wrapper.get('[data-test="key-save-11"]').attributes()).not.toHaveProperty('disabled')
    await wrapper.get('[data-test="key-save-11"]').trigger('click')
    await flushPromises()

    expect(requestMock).toHaveBeenCalledWith('/api/groups/7/keys/11', {
      method: 'PUT',
      json: { weight_manual: 50 },
      signal: expect.any(AbortSignal),
    })
    expect(invalidate.mock.calls.map(([filters]) => filters)).toEqual([
      { queryKey: controlQueryKeys.groups.keys(7), exact: true },
      { queryKey: controlQueryKeys.groups.detail(7), exact: true },
      { queryKey: controlQueryKeys.groups.list(), exact: true },
      { queryKey: controlQueryKeys.health(), exact: true },
    ])
    expect(client.getMutationCache().getAll()).toHaveLength(0)
    wrapper.unmount()
  })

  it('deletes through an accessible dialog, restores trigger focus, and uses the exact invalidation set', async () => {
    let resolveDelete!: () => void
    let deleteSignal: AbortSignal | null | undefined
    const deleteResult = new Promise<void>((resolve) => {
      resolveDelete = resolve
    })
    const requestMock = vi.fn(async (path: string, options?: ApiRequestOptions) => {
      if (path === '/api/groups/7/keys' && options?.method === 'GET') return keys
      if (path === '/api/groups/7/keys/11' && options?.method === 'DELETE') {
        deleteSignal = options.signal
        return deleteResult
      }
      throw new Error(`unexpected request: ${path}`)
    })
    const { queryClient: client, wrapper } = await mountKeys(requestMock as ApiClient['request'])
    const invalidate = vi.spyOn(client, 'invalidateQueries')
    const trigger = wrapper.get('[data-test="key-delete-11"]')

    await trigger.trigger('click')
    await flushPromises()
    const dialog = document.querySelector('[role="dialog"]')
    expect(dialog).not.toBeNull()
    expect(dialog?.getAttribute('aria-labelledby')).toBeTruthy()
    expect(document.querySelector('[data-test="key-delete-confirm-11"]')?.classList).toContain(
      'app-button--secondary',
    )
    clickDocument('[data-test="key-delete-cancel-11"]')
    await flushPromises()
    expect(document.activeElement).toBe(trigger.element)

    await trigger.trigger('click')
    await flushPromises()
    clickDocument('[data-test="key-delete-confirm-11"]')
    await flushPromises()
    expect(requestMock).toHaveBeenCalledWith('/api/groups/7/keys/11', {
      method: 'DELETE',
      signal: expect.any(AbortSignal),
    })
    const close = document.querySelector<HTMLButtonElement>('.app-dialog__close')
    expect(close?.disabled).toBe(true)
    expect(
      document.querySelector<HTMLButtonElement>('[data-test="key-delete-cancel-11"]')?.disabled,
    ).toBe(true)
    close?.click()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    document.querySelector<HTMLElement>('.app-dialog__overlay')?.click()
    await flushPromises()
    expect(document.querySelector('[role="dialog"]')).not.toBeNull()
    expect(deleteSignal?.aborted).toBe(false)

    resolveDelete()
    await flushPromises()
    expect(document.activeElement).toBe(trigger.element)
    expect(invalidate.mock.calls.map(([filters]) => filters)).toEqual([
      { queryKey: controlQueryKeys.groups.keys(7), exact: true },
      { queryKey: controlQueryKeys.groups.detail(7), exact: true },
      { queryKey: controlQueryKeys.groups.list(), exact: true },
      { queryKey: controlQueryKeys.health(), exact: true },
    ])
    expect(client.getMutationCache().getAll()).toHaveLength(0)
    wrapper.unmount()
  })
})
