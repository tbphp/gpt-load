import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import type { ApiClient } from '@/api/client'
import type { AccessKeyDto, GroupSummary } from '@/api/control/types'
import { controlQueryKeys } from '@/app/query-keys'
import { mountApp } from '@/test/mount-app'

import AccessKeyDrawer from './AccessKeyDrawer.vue'

const canary = 'sk-gl-ACCESS_KEY_DRAWER_CANARY'
const groups: GroupSummary[] = [
  {
    id: 7,
    name: 'Primary',
    upstream_url: 'https://api.example.com',
    protocols: ['openai', 'openai-response'],
    models: [{ id: 'gpt-4.1', alias: 'public-gpt' }],
    enabled: true,
    key_count: 1,
  },
]
const existing: AccessKeyDto = {
  id: 9,
  name: 'client',
  key: canary,
  status: 'active',
  filters: { groups: [7], protocols: ['openai-response'], models: ['legacy-free'] },
  rpm_limit: 12,
}

function queryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

async function mountDrawer(
  request: ApiClient['request'],
  props: { open: boolean; accessKey?: AccessKeyDto | null } = { open: true },
) {
  const client = queryClient()
  const mounted = await mountApp(AccessKeyDrawer, {
    api: { request },
    queryClient: client,
    locale: 'en-US',
    mounting: {
      props: { open: props.open, accessKey: props.accessKey ?? null, groups },
      attachTo: document.body,
    },
  })
  await flushPromises()
  return { ...mounted, queryClient: client }
}

function element<T extends Element>(selector: string): T {
  const found = document.querySelector<T>(selector)
  if (!found) throw new Error(`missing ${selector}`)
  return found
}

describe('AccessKeyDrawer', () => {
  it('creates without status, keeps plaintext local, invalidates only the list, and clears on close', async () => {
    const request = vi.fn().mockResolvedValue({
      id: 10,
      name: 'new-client',
      key: canary,
      status: 'active',
      filters: { groups: [], protocols: [], models: [] },
      rpm_limit: 0,
    })
    const { queryClient: client, wrapper } = await mountDrawer(request as ApiClient['request'])
    const invalidate = vi.spyOn(client, 'invalidateQueries').mockResolvedValue()
    expect(document.activeElement).toBe(element<HTMLInputElement>('[data-test="access-key-name"]'))
    expect(document.querySelector('[data-test="access-key-status"]')).toBeNull()

    element<HTMLInputElement>('[data-test="access-key-name"]').value = ' new-client '
    element<HTMLInputElement>('[data-test="access-key-name"]').dispatchEvent(
      new Event('input', { bubbles: true }),
    )
    await flushPromises()
    element<HTMLButtonElement>('[data-test="access-key-save"]').click()
    await flushPromises()

    expect(request).toHaveBeenCalledWith('/api/access-keys', {
      method: 'POST',
      json: {
        name: 'new-client',
        filters: { groups: [], protocols: [], models: [] },
        rpm_limit: 0,
      },
      signal: expect.any(AbortSignal),
    })
    expect((request.mock.calls[0]?.[1] as { json: object }).json).not.toHaveProperty('status')
    expect(invalidate.mock.calls.map(([filters]) => filters)).toEqual([
      { queryKey: controlQueryKeys.accessKeys.list() },
    ])
    expect(client.getMutationCache().getAll()).toHaveLength(0)
    expect(document.body.textContent).not.toContain(canary)

    element<HTMLButtonElement>('[data-test="access-key-result-reveal"]').click()
    await flushPromises()
    expect(document.body.textContent).toContain(canary)

    await wrapper.setProps({ open: false })
    await flushPromises()
    expect(document.body.textContent).not.toContain(canary)
    expect(JSON.stringify(wrapper.emitted())).not.toContain(canary)
    await wrapper.setProps({ open: true })
    await flushPromises()
    expect(element<HTMLInputElement>('[data-test="access-key-name"]').value).toBe('')
    expect(document.body.textContent).not.toContain(canary)
    wrapper.unmount()
  })

  it('edits with a normalized dirty-only patch, preserves free-entry models, and blocks empty PUT', async () => {
    const request = vi
      .fn()
      .mockResolvedValue({ ...existing, status: 'disabled' }) as ApiClient['request']
    const { queryClient: client, wrapper } = await mountDrawer(request, {
      open: true,
      accessKey: existing,
    })
    const invalidate = vi.spyOn(client, 'invalidateQueries').mockResolvedValue()

    expect(element<HTMLSelectElement>('[data-test="access-key-status"]').value).toBe('active')
    expect(document.body.textContent).toContain('legacy-free')
    element<HTMLButtonElement>('[data-test="access-key-save"]').click()
    expect(request).not.toHaveBeenCalled()

    const status = element<HTMLSelectElement>('[data-test="access-key-status"]')
    status.value = 'disabled'
    status.dispatchEvent(new Event('change', { bubbles: true }))
    await flushPromises()
    element<HTMLButtonElement>('[data-test="access-key-save"]').click()
    await flushPromises()

    expect(request).toHaveBeenCalledWith('/api/access-keys/9', {
      method: 'PUT',
      json: { status: 'disabled' },
      signal: expect.any(AbortSignal),
    })
    expect(invalidate.mock.calls.map(([filters]) => filters)).toEqual([
      { queryKey: controlQueryKeys.accessKeys.list() },
    ])
    wrapper.unmount()
  })

  it('keeps Save disabled and sends no PUT after a filter is toggled off and back on', async () => {
    const reordered: AccessKeyDto = {
      ...existing,
      filters: {
        groups: [7],
        protocols: ['openai-response', 'openai'],
        models: ['legacy-free', 'gpt-4.1'],
      },
    }
    const request = vi.fn() as ApiClient['request']
    const { wrapper } = await mountDrawer(request, { open: true, accessKey: reordered })
    const protocolCheckboxes = document.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')
    const openAIResponse = protocolCheckboxes[4]
    if (!openAIResponse) throw new Error('missing openai-response checkbox')

    openAIResponse.checked = false
    openAIResponse.dispatchEvent(new Event('change', { bubbles: true }))
    await flushPromises()
    openAIResponse.checked = true
    openAIResponse.dispatchEvent(new Event('change', { bubbles: true }))
    await flushPromises()

    const save = element<HTMLButtonElement>('[data-test="access-key-save"]')
    expect(save.disabled).toBe(true)
    save.click()
    expect(request).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('requires a non-negative safe-integer RPM and retains input after generic errors', async () => {
    const request = vi.fn().mockRejectedValue(new Error(canary)) as ApiClient['request']
    const { wrapper } = await mountDrawer(request)
    const name = element<HTMLInputElement>('[data-test="access-key-name"]')
    const rpm = element<HTMLInputElement>('[data-test="access-key-rpm"]')
    name.value = 'client'
    name.dispatchEvent(new Event('input', { bubbles: true }))
    rpm.value = '1.5'
    rpm.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    expect(element<HTMLButtonElement>('[data-test="access-key-save"]').disabled).toBe(true)

    rpm.value = '0'
    rpm.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    element<HTMLButtonElement>('[data-test="access-key-save"]').click()
    await flushPromises()
    expect(element<HTMLInputElement>('[data-test="access-key-name"]').value).toBe('client')
    expect(document.body.textContent).not.toContain(canary)
    wrapper.unmount()
  })

  it('ignores a late plaintext response after the Drawer closes', async () => {
    let resolveRequest!: (value: AccessKeyDto) => void
    const request = vi.fn(
      () =>
        new Promise<AccessKeyDto>((resolve) => {
          resolveRequest = resolve
        }),
    ) as ApiClient['request']
    const { queryClient: client, wrapper } = await mountDrawer(request)
    const invalidate = vi.spyOn(client, 'invalidateQueries').mockResolvedValue()
    const name = element<HTMLInputElement>('[data-test="access-key-name"]')
    name.value = 'late-client'
    name.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    element<HTMLButtonElement>('[data-test="access-key-save"]').click()
    await flushPromises()

    await wrapper.setProps({ open: false })
    resolveRequest({
      id: 11,
      name: 'late-client',
      key: canary,
      status: 'active',
      filters: { groups: [], protocols: [], models: [] },
      rpm_limit: 0,
    })
    await flushPromises()

    expect(invalidate).not.toHaveBeenCalled()
    expect(document.body.textContent).not.toContain(canary)
    wrapper.unmount()
  })

  it('ignores an ordinary late rejection after the Drawer closes', async () => {
    let rejectRequest!: (error: unknown) => void
    const request = vi.fn(
      () =>
        new Promise<AccessKeyDto>((_, reject) => {
          rejectRequest = reject
        }),
    ) as ApiClient['request']
    const { queryClient: client, wrapper } = await mountDrawer(request)
    const invalidate = vi.spyOn(client, 'invalidateQueries').mockResolvedValue()
    const setupState = wrapper.vm.$.setupState as { failed: boolean }
    const name = element<HTMLInputElement>('[data-test="access-key-name"]')
    name.value = 'late-reject-client'
    name.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    element<HTMLButtonElement>('[data-test="access-key-save"]').click()
    await flushPromises()

    await wrapper.setProps({ open: false })
    rejectRequest(new Error(canary))
    await flushPromises()

    expect(setupState.failed).toBe(false)
    expect(invalidate).not.toHaveBeenCalled()
    const snapshotSafeOutput = JSON.stringify({
      render: document.body.innerHTML,
      emitted: wrapper.emitted(),
    })
    expect(snapshotSafeOutput).not.toContain(canary)
    expect(snapshotSafeOutput).not.toContain('Unable to save')
    wrapper.unmount()
  })

  it('ignores an ordinary late rejection after unmount', async () => {
    let rejectRequest!: (error: unknown) => void
    const request = vi.fn(
      () =>
        new Promise<AccessKeyDto>((_, reject) => {
          rejectRequest = reject
        }),
    ) as ApiClient['request']
    const { queryClient: client, wrapper } = await mountDrawer(request)
    const invalidate = vi.spyOn(client, 'invalidateQueries').mockResolvedValue()
    const setupState = wrapper.vm.$.setupState as { failed: boolean }
    const emitted = wrapper.emitted()
    const name = element<HTMLInputElement>('[data-test="access-key-name"]')
    name.value = 'unmounted-reject-client'
    name.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    element<HTMLButtonElement>('[data-test="access-key-save"]').click()
    await flushPromises()

    wrapper.unmount()
    rejectRequest(new Error(canary))
    await flushPromises()

    expect(setupState.failed).toBe(false)
    expect(invalidate).not.toHaveBeenCalled()
    const snapshotSafeOutput = JSON.stringify({ render: document.body.innerHTML, emitted })
    expect(snapshotSafeOutput).not.toContain(canary)
    expect(snapshotSafeOutput).not.toContain('Unable to save')
  })
})
