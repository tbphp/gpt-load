import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import type { ApiClient } from '@/api/client'
import type { AccessKeyDto } from '@/api/control/types'
import { mountApp } from '@/test/mount-app'

import AccessKeyDeleteDialog from './AccessKeyDeleteDialog.vue'

const accessKey: AccessKeyDto = {
  id: 9,
  name: 'client',
  masked_key: 'sk-gl-••••••••••••',
  status: 'active',
  filters: { groups: [], protocols: [], models: [] },
  rpm_limit: 0,
  created_at: '2026-07-28T00:00:00Z',
  updated_at: '2026-07-28T00:00:00Z',
}

function documentButton(selector: string): HTMLButtonElement {
  const button = document.querySelector<HTMLButtonElement>(selector)
  if (!button) throw new Error(`missing ${selector}`)
  return button
}

describe('AccessKeyDeleteDialog', () => {
  it('warns for the last key, deletes the exact path, and emits deletion without refreshing', async () => {
    const request = vi.fn().mockResolvedValue(undefined) as ApiClient['request']
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { wrapper } = await mountApp(AccessKeyDeleteDialog, {
      api: { request },
      queryClient: client,
      locale: 'en-US',
      mounting: { props: { accessKey, total: 1 }, attachTo: document.body },
    })
    const invalidate = vi.spyOn(client, 'invalidateQueries')

    await wrapper.get('[data-test="access-key-delete-open"]').trigger('click')
    await flushPromises()
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('last AccessKey')
    documentButton('[data-test="access-key-delete-confirm"]').click()
    await flushPromises()

    expect(request).toHaveBeenCalledWith('/api/access-keys/9', {
      method: 'DELETE',
      signal: expect.any(AbortSignal),
    })
    expect(wrapper.emitted('deleted')).toEqual([[]])
    expect(invalidate).not.toHaveBeenCalled()
    expect(client.getMutationCache().getAll()).toHaveLength(0)
    wrapper.unmount()
  })

  it('uses a generic error without exposing credential-like error details', async () => {
    const request = vi
      .fn()
      .mockRejectedValue(new Error('sk-gl-ERROR_CANARY')) as ApiClient['request']
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { wrapper } = await mountApp(AccessKeyDeleteDialog, {
      api: { request },
      queryClient: client,
      locale: 'en-US',
      mounting: { props: { accessKey, total: 2 }, attachTo: document.body },
    })

    await wrapper.get('[data-test="access-key-delete-open"]').trigger('click')
    await flushPromises()
    expect(document.querySelector('[role="dialog"]')?.textContent).not.toContain('last AccessKey')
    documentButton('[data-test="access-key-delete-confirm"]').click()
    await flushPromises()
    expect(document.body.textContent).toContain('Unable to delete')
    expect(document.body.textContent).not.toContain('sk-gl-ERROR_CANARY')
    expect(wrapper.emitted('deleted')).toBeUndefined()
    wrapper.unmount()
  })
})
