import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import type { ApiClient } from '@/api/client'
import type { AccessKeyDto, GroupSummary } from '@/api/control/types'
import { controlQueryKeys } from '@/app/query-keys'
import { mountApp } from '@/test/mount-app'

import AccessKeyDrawer from './AccessKeyDrawer.vue'
import type { AccessKeyDraft } from './access-key-patch'

const canary = 'sk-gl-ACCESS_KEY_DRAWER_CANARY'
const groups: GroupSummary[] = [
  {
    id: 7,
    name: 'Primary',
    upstream_url: 'https://api.example.com',
    protocols: ['openai'],
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

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, reject, resolve }
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

function protocolLabel(name: string): HTMLLabelElement | undefined {
  return [...document.querySelectorAll<HTMLLabelElement>('.access-key-drawer__check')].find(
    (label) => label.textContent?.includes(name),
  )
}

describe('AccessKeyDrawer', () => {
  it('does not offer the reserved protocol while creating an AccessKey', async () => {
    const request = vi.fn() as ApiClient['request']
    const { wrapper } = await mountDrawer(request)

    expect(protocolLabel('OpenAI')).toBeDefined()
    expect(protocolLabel('Anthropic')).toBeDefined()
    expect(protocolLabel('Gemini')).toBeDefined()
    expect(protocolLabel('OpenAI Responses')).toBeUndefined()
    wrapper.unmount()
  })

  it('shows a retained historical reserved protocol checked with a disabled hint', async () => {
    const request = vi.fn() as ApiClient['request']
    const { wrapper } = await mountDrawer(request, { open: true, accessKey: existing })
    const reserved = protocolLabel('OpenAI Responses')

    expect(reserved).toBeDefined()
    expect(reserved?.querySelector<HTMLInputElement>('input')?.checked).toBe(true)
    expect(reserved?.textContent).toContain(
      'Currently disabled; retained only for this historical AccessKey.',
    )
    wrapper.unmount()
  })

  it('does not render or submit an injected reserved protocol for an ordinary edit', async () => {
    const ordinary: AccessKeyDto = {
      ...existing,
      filters: { ...existing.filters, protocols: ['openai'] },
    }
    const request = vi.fn() as ApiClient['request']
    const { wrapper } = await mountDrawer(request, { open: true, accessKey: ordinary })

    expect(protocolLabel('OpenAI Responses')).toBeUndefined()
    const setupState = wrapper.vm.$.setupState as { draft: AccessKeyDraft }
    setupState.draft.filters.protocols = ['openai', 'openai-response']
    await flushPromises()

    expect(element<HTMLButtonElement>('[data-test="access-key-save"]').disabled).toBe(true)
    element<HTMLFormElement>('.access-key-drawer').dispatchEvent(
      new Event('submit', { bubbles: true, cancelable: true }),
    )
    await flushPromises()
    expect(request).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('sends an explicit filter update when a historical reserved protocol is removed', async () => {
    const saved: AccessKeyDto = {
      ...existing,
      filters: { ...existing.filters, protocols: [] },
    }
    const request = vi.fn().mockResolvedValue(saved) as ApiClient['request']
    const { wrapper } = await mountDrawer(request, { open: true, accessKey: existing })
    const reserved = protocolLabel('OpenAI Responses')?.querySelector<HTMLInputElement>('input')
    if (!reserved) throw new Error('missing historical openai-response checkbox')

    reserved.checked = false
    reserved.dispatchEvent(new Event('change', { bubbles: true }))
    await flushPromises()
    element<HTMLButtonElement>('[data-test="access-key-save"]').click()
    await flushPromises()

    expect(request).toHaveBeenCalledWith('/api/access-keys/9', {
      method: 'PUT',
      json: {
        filters: {
          groups: [7],
          protocols: [],
          models: ['legacy-free'],
        },
      },
      signal: expect.any(AbortSignal),
    })
    wrapper.unmount()
  })

  it('completes one create cycle with one local result and starts fresh only after close', async () => {
    const firstCreated: AccessKeyDto = {
      id: 10,
      name: 'new-client',
      key: canary,
      status: 'active',
      filters: { groups: [], protocols: [], models: [] },
      rpm_limit: 0,
    }
    const secondCreated: AccessKeyDto = {
      ...firstCreated,
      id: 11,
      name: 'next-client',
      key: 'sk-gl-SECOND_ACCESS_KEY_CANARY',
    }
    const request = vi.fn().mockResolvedValueOnce(firstCreated).mockResolvedValueOnce(secondCreated)
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
    expect(
      JSON.stringify(
        client
          .getQueryCache()
          .getAll()
          .map((query) => query.state.data),
      ),
    ).not.toContain(canary)
    expect(document.body.textContent).not.toContain(canary)

    expect(document.querySelector('[data-test="access-key-name"]')).toBeNull()
    expect(document.querySelector('[data-test="access-key-model-input"]')).toBeNull()
    expect(document.querySelector('[data-test="access-key-rpm"]')).toBeNull()
    expect(document.querySelector('[data-test="access-key-save"]')).toBeNull()
    expect(document.querySelectorAll('.access-key-drawer fieldset')).toHaveLength(0)
    element<HTMLButtonElement>('[data-test="access-key-result-reveal"]').click()
    await flushPromises()
    expect(document.body.textContent).toContain(canary)
    expect(
      document.querySelector<HTMLButtonElement>('button[aria-label="Copy AccessKey"]'),
    ).not.toBeNull()

    element<HTMLFormElement>('.access-key-drawer').dispatchEvent(
      new Event('submit', { bubbles: true, cancelable: true }),
    )
    await flushPromises()
    expect(request).toHaveBeenCalledTimes(1)

    const completedActions = document.querySelectorAll<HTMLButtonElement>(
      '.access-key-drawer__actions button',
    )
    expect(completedActions).toHaveLength(1)
    expect(completedActions[0]?.textContent?.trim()).toBe('Close')
    expect(completedActions[0]?.classList).toContain('app-button--secondary')
    expect(completedActions[0]?.disabled).toBe(false)
    completedActions[0]?.click()
    expect(wrapper.emitted('update:open')?.at(-1)).toEqual([false])
    await wrapper.setProps({ open: false })
    await flushPromises()
    expect(document.body.textContent).not.toContain(canary)
    expect(JSON.stringify(wrapper.emitted())).not.toContain(canary)

    await wrapper.setProps({ open: true })
    await flushPromises()
    expect(element<HTMLInputElement>('[data-test="access-key-name"]').value).toBe('')
    expect(document.body.textContent).not.toContain(canary)
    element<HTMLInputElement>('[data-test="access-key-name"]').value = 'next-client'
    element<HTMLInputElement>('[data-test="access-key-name"]').dispatchEvent(
      new Event('input', { bubbles: true }),
    )
    await flushPromises()
    element<HTMLButtonElement>('[data-test="access-key-save"]').click()
    await flushPromises()
    expect(request).toHaveBeenCalledTimes(2)
    expect(request.mock.calls[1]?.[0]).toBe('/api/access-keys')
    expect(invalidate).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('keeps a historical reserved filter while edit mutation controls remain reusable', async () => {
    const request = vi
      .fn()
      .mockResolvedValueOnce({ ...existing, status: 'disabled' })
      .mockResolvedValueOnce({
        ...existing,
        status: 'disabled',
        rpm_limit: 24,
      }) as ApiClient['request']
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

    expect(document.querySelector('[data-test="access-key-name"]')).not.toBeNull()
    expect(document.querySelector('[data-test="access-key-model-input"]')).not.toBeNull()
    expect(document.querySelector('[data-test="access-key-save"]')).not.toBeNull()
    const rpm = element<HTMLInputElement>('[data-test="access-key-rpm"]')
    rpm.value = '24'
    rpm.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    element<HTMLButtonElement>('[data-test="access-key-save"]').click()
    await flushPromises()

    expect(request).toHaveBeenNthCalledWith(2, '/api/access-keys/9', {
      method: 'PUT',
      json: { rpm_limit: 24 },
      signal: expect.any(AbortSignal),
    })
    expect(invalidate).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it.each(['resolve', 'reject'] as const)(
    'keeps create B pending and isolated when create A settles late by %s',
    async (outcome) => {
      const createA = deferred<AccessKeyDto>()
      const createB = deferred<AccessKeyDto>()
      const request = vi
        .fn()
        .mockImplementationOnce(() => createA.promise)
        .mockImplementationOnce(() => createB.promise)
      const { wrapper } = await mountDrawer(request as ApiClient['request'])
      const setupState = wrapper.vm.$.setupState as { failed: boolean; pending: boolean }

      const nameA = element<HTMLInputElement>('[data-test="access-key-name"]')
      nameA.value = 'session-a'
      nameA.dispatchEvent(new Event('input', { bubbles: true }))
      await flushPromises()
      element<HTMLButtonElement>('[data-test="access-key-save"]').click()
      await flushPromises()
      const signalA = request.mock.calls[0]?.[1]?.signal
      expect(signalA?.aborted).toBe(false)

      await wrapper.setProps({ open: false })
      await flushPromises()
      expect(signalA?.aborted).toBe(true)
      await wrapper.setProps({ open: true })
      await flushPromises()

      const nameB = element<HTMLInputElement>('[data-test="access-key-name"]')
      nameB.value = 'session-b'
      nameB.dispatchEvent(new Event('input', { bubbles: true }))
      await flushPromises()
      element<HTMLButtonElement>('[data-test="access-key-save"]').click()
      await flushPromises()
      expect(request).toHaveBeenCalledTimes(2)
      expect(nameB.disabled).toBe(true)
      expect(element<HTMLButtonElement>('[data-test="access-key-save"]').disabled).toBe(true)

      if (outcome === 'resolve') {
        createA.resolve({
          id: 12,
          name: 'session-a',
          key: 'late-a-secret-canary',
          status: 'active',
          filters: { groups: [], protocols: [], models: [] },
          rpm_limit: 0,
        })
      } else {
        createA.reject(new Error('late-a-error-canary'))
      }
      await flushPromises()

      expect(element<HTMLInputElement>('[data-test="access-key-name"]').value).toBe('session-b')
      expect(element<HTMLInputElement>('[data-test="access-key-name"]').disabled).toBe(true)
      expect(element<HTMLButtonElement>('[data-test="access-key-save"]').disabled).toBe(true)
      expect(setupState.pending).toBe(true)
      expect(setupState.failed).toBe(false)
      expect(document.querySelector('[data-test="access-key-result-reveal"]')).toBeNull()
      expect(document.body.textContent).not.toContain('late-a')
      expect(document.body.textContent).not.toContain('Unable to save')

      createB.resolve({
        id: 13,
        name: 'session-b',
        key: 'session-b-secret-canary',
        status: 'active',
        filters: { groups: [], protocols: [], models: [] },
        rpm_limit: 0,
      })
      await flushPromises()

      expect(setupState.pending).toBe(false)
      expect(document.querySelector('[data-test="access-key-name"]')).toBeNull()
      element<HTMLButtonElement>('[data-test="access-key-result-reveal"]').click()
      await flushPromises()
      expect(
        element<HTMLElement>('.access-key-drawer__secret .secret-value code').textContent,
      ).toBe('session-b-secret-canary')
      const completedClose = element<HTMLButtonElement>('.access-key-drawer__actions button')
      expect(completedClose.textContent?.trim()).toBe('Close')
      expect(completedClose.disabled).toBe(false)
      wrapper.unmount()
    },
  )

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
