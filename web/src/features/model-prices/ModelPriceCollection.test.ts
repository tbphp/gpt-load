import { QueryClient } from '@tanstack/vue-query'

import type { ModelPriceRuleDto } from '@/app/resources/model-prices'
import { mountApp } from '@/test/mount-app'

import ModelPriceCollection from './ModelPriceCollection.vue'

const rule: ModelPriceRuleDto = {
  pattern: `vendor-${'segment-'.repeat(14)}*`,
  source: 'builtin',
  prices: {
    uncached_input: null,
    cache_read: 0,
    cache_write_5m: 1.25,
    cache_write_1h: null,
    output: 8,
  },
  source_url: null,
  updated_at: '2026-07-29T00:00:00Z',
  pricing_policy: null,
}

describe('ModelPriceCollection', () => {
  it('renders mobile identity and action outside native price details with a plain missing source', async () => {
    vi.stubGlobal('matchMedia', () => ({
      matches: true,
      media: '(max-width: 767px)',
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }))
    const { wrapper } = await mountApp(ModelPriceCollection, {
      api: { request: vi.fn() },
      queryClient: new QueryClient(),
      locale: 'en-US',
      mounting: {
        props: {
          rules: [rule],
          source: 'builtin',
        },
      },
    })

    const card = wrapper.get('[data-test="builtin-price-card-0"]')
    expect(card.get('h3').text()).toBe(rule.pattern)
    expect(card.get('[data-test="builtin-price-edit-0"]').text()).toContain('Create override')
    expect(card.get('details').attributes()).not.toHaveProperty('open')
    expect(card.get('[data-test="builtin-source-0"]').element.tagName).toBe('SPAN')
    expect(card.find('a[data-test="builtin-source-0"]').exists()).toBe(false)
    expect(card.get('time').attributes('datetime')).toBe('2026-07-29T00:00:00Z')

    wrapper.unmount()
    vi.unstubAllGlobals()
  })
})
