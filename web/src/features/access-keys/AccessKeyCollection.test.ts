import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import type { ApiClient, ApiRequestOptions } from '@/api/client'
import type { AccessKeyDto, GroupSummary } from '@/api/control/types'
import { mountApp } from '@/test/mount-app'

import AccessKeyCollection from './AccessKeyCollection.vue'

const plaintext = 'sk-gl-RESPONSIVE-CANARY'
const accessKeys: AccessKeyDto[] = [
  {
    id: 9,
    name: `client-${'segment-'.repeat(14)}`,
    masked_key: 'sk-gl-••••••••cafe',
    status: 'active',
    filters: { groups: [], protocols: [], models: [] },
    rpm_limit: 0,
    created_at: '2026-07-28T00:00:00Z',
    updated_at: '2026-07-29T00:00:00Z',
  },
]
const groups: GroupSummary[] = []

function mediaQuery(initialMatches: boolean) {
  let matches = initialMatches
  const listeners = new Set<(event: MediaQueryListEvent) => void>()
  const media = {
    get matches() {
      return matches
    },
    media: '(max-width: 767px)',
    onchange: null,
    addEventListener: (_type: string, listener: (event: MediaQueryListEvent) => void) =>
      listeners.add(listener),
    removeEventListener: (_type: string, listener: (event: MediaQueryListEvent) => void) =>
      listeners.delete(listener),
    addListener: vi.fn(),
    removeListener: vi.fn(),
    dispatchEvent: vi.fn(),
    change(next: boolean) {
      matches = next
      for (const listener of listeners) listener({ matches: next } as MediaQueryListEvent)
    },
  }
  return media
}

describe('AccessKeyCollection', () => {
  it('uses one reveal owner while switching between mobile cards and the desktop table', async () => {
    const media = mediaQuery(true)
    vi.stubGlobal('matchMedia', () => media)
    const request = vi.fn(async (path: string, options?: ApiRequestOptions) => {
      if (path === '/api/access-keys/9/reveal' && options?.method === 'POST') {
        return { id: 9, key: plaintext, revealed_at: '2026-07-29T00:01:00Z' }
      }
      throw new Error(`unexpected ${path}`)
    }) as ApiClient['request']
    const { wrapper } = await mountApp(AccessKeyCollection, {
      api: { request },
      queryClient: new QueryClient(),
      locale: 'en-US',
      mounting: {
        props: { accessKeys, groups },
      },
    })

    expect(wrapper.find('[data-test="access-key-card-9"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="access-key-row-9"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain(plaintext)

    await wrapper.get('[data-test="access-key-reveal-9"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain(plaintext)

    media.change(false)
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-test="access-key-card-9"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="access-key-row-9"]').text()).toContain(plaintext)
    expect(request).toHaveBeenCalledTimes(1)
    wrapper.unmount()
    vi.unstubAllGlobals()
  })
})
