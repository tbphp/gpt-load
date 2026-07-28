import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'
import { createMemoryHistory } from 'vue-router'

import type { ApiClient, ApiRequestOptions } from '@/api/client'
import type { ModelPriceRuleDto } from '@/api/control/model-prices'
import { controlQueryKeys } from '@/app/query-keys'
import { createAppRouter } from '@/app/router'
import { mountApp } from '@/test/mount-app'

import ModelPricesView from './ModelPricesView.vue'

const builtin: ModelPriceRuleDto = {
  pattern: 'gpt-5.6',
  source: 'builtin',
  prices: {
    uncached_input: 5,
    cache_read: 0,
    cache_write_5m: null,
    cache_write_1h: null,
    output: 30,
  },
  source_url: 'https://developers.openai.com/api/docs/pricing',
  updated_at: '2026-07-26T00:00:00Z',
  pricing_policy: {
    input_threshold_tokens: 272000,
    input_multiplier: 2,
    output_multiplier: 1.5,
  },
}
const override: ModelPriceRuleDto = {
  pattern: 'vendor-*',
  source: 'user',
  prices: {
    uncached_input: null,
    cache_read: 0,
    cache_write_5m: 1.25,
    cache_write_1h: null,
    output: 8,
  },
  source_url: null,
  updated_at: '2026-07-27T00:00:00Z',
  pricing_policy: null,
}
const exactOverride: ModelPriceRuleDto = {
  ...override,
  pattern: 'vendor-model',
}
const globalOverride: ModelPriceRuleDto = {
  ...override,
  pattern: '*',
}

function priceReport(
  builtinRules: ModelPriceRuleDto[] = [builtin],
  overrides: ModelPriceRuleDto[] = [override],
) {
  return {
    price_unit: 'usd_per_million_tokens' as const,
    builtin: builtinRules,
    overrides,
  }
}

function queryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

async function mountView(request: ApiClient['request']) {
  const client = queryClient()
  const mounted = await mountApp(ModelPricesView, {
    api: { request },
    queryClient: client,
    locale: 'en-US',
    path: '/settings/model-prices',
    mounting: { attachTo: document.body },
  })
  await flushPromises()
  return { ...mounted, queryClient: client }
}

describe('ModelPricesView', () => {
  it('owns the protected nested route and renders builtin and override tables without merging null and zero', async () => {
    const router = createAppRouter({ hasCredential: () => true }, createMemoryHistory())
    expect(router.resolve('/settings/model-prices').matched.at(-1)?.components?.default).toBe(
      ModelPricesView,
    )

    const request = vi.fn(async (path: string, options?: ApiRequestOptions) => {
      if (path === '/api/model-prices' && options?.method === 'GET') return priceReport()
      throw new Error(`unexpected ${path}`)
    }) as ApiClient['request']
    const { wrapper } = await mountView(request)

    expect(wrapper.get('h1').text()).toBe('Model prices')
    expect(wrapper.findAll('table')).toHaveLength(2)
    expect(wrapper.get('[data-test="builtin-price-row-0"]').text()).toContain('Built-in')
    expect(wrapper.get('[data-test="override-price-row-0"]').text()).toContain('Override')
    expect(wrapper.get('[data-test="builtin-0-cache_read"]').text()).toContain('$0')
    expect(wrapper.get('[data-test="builtin-0-cache_read"]').text()).toContain('Explicitly free')
    expect(wrapper.get('[data-test="builtin-0-cache_write_5m"]').text()).toBe('Not configured')
    expect(wrapper.get('[data-test="override-0-uncached_input"]').text()).toBe('Not configured')
    expect(wrapper.get('[data-test="builtin-0-output"]').text()).toBe('$30 / 1M')
    expect(wrapper.get('[data-test="override-0-output"]').text()).toBe('$8 / 1M')
    expect(wrapper.get('[data-test="override-source-0"]').text()).toContain('Override')
    const policy = wrapper.get('[data-test="builtin-pricing-policy-0"]')
    expect(policy.text()).toContain('Long-context policy')
    expect(policy.text()).toContain('More than 272,000 input tokens')
    expect(policy.text()).toContain('input prices ×2')
    expect(policy.text()).toContain('output price ×1.5')
    expect(policy.find('button').exists()).toBe(false)
    expect(policy.find('input').exists()).toBe(false)
    expect(wrapper.findAll('[data-test^="builtin-pricing-policy-"]')).toHaveLength(1)
    expect(wrapper.get('time[datetime="2026-07-26T00:00:00Z"]')).toBeDefined()
    expect(wrapper.get('time[datetime="2026-07-27T00:00:00Z"]')).toBeDefined()
    const source = wrapper.get<HTMLAnchorElement>('[data-test="builtin-source-0"]')
    expect(source.attributes('href')).toBe(builtin.source_url)
    expect(source.attributes('target')).toBe('_blank')
    expect(source.attributes('rel')).toContain('noopener')
    expect(wrapper.text()).toContain('Historical usage and cost are not recalculated')
    expect(wrapper.text()).toContain('upstream model ID')
    expect(wrapper.text()).toContain('same model ID shares one global price')
    expect(wrapper.text()).toContain(
      'User overrides take precedence over every built-in exact and prefix rule',
    )
    expect(wrapper.text()).toContain('replaces the whole five-slot rule')
    const orderedSections = wrapper
      .findAll('[data-test="model-price-rule-section"]')
      .map((section) => section.attributes('data-source'))
    expect(orderedSections).toEqual(['user', 'builtin'])
    wrapper.unmount()
  })

  it('opens add, builtin-prefill, and user-edit drawers from their visible actions', async () => {
    const request = vi.fn().mockResolvedValue(priceReport()) as ApiClient['request']
    const { wrapper } = await mountView(request)

    await wrapper.get('[data-test="model-price-add"]').trigger('click')
    await flushPromises()
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(
      'Add model price override',
    )
    document.querySelector<HTMLButtonElement>('[aria-label="Close model price editor"]')?.click()
    await flushPromises()

    await wrapper.get('[data-test="builtin-price-edit-0"]').trigger('click')
    await flushPromises()
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(
      'Create override from built-in price',
    )
    expect(
      document.querySelector<HTMLInputElement>('[data-test="model-price-pattern"]')?.readOnly,
    ).toBe(true)
    document.querySelector<HTMLButtonElement>('[aria-label="Close model price editor"]')?.click()
    await flushPromises()

    await wrapper.get('[data-test="override-price-edit-0"]').trigger('click')
    await flushPromises()
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(
      'Edit model price override',
    )
    wrapper.unmount()
  })

  it('marks a bare star row as a global user override', async () => {
    const request = vi.fn().mockResolvedValue(priceReport([builtin], [globalOverride]))
    const { wrapper } = await mountView(request as ApiClient['request'])

    const warning = wrapper.get('[data-test="model-price-global-row-warning"]')
    expect(warning.text()).toContain('Global user override')
    expect(warning.find('svg').exists()).toBe(true)
    wrapper.unmount()
  })

  it('distinguishes loading, initial error, and a genuinely empty price table', async () => {
    let resolveRequest!: (value: unknown) => void
    const pending = vi.fn(
      () =>
        new Promise((resolve) => {
          resolveRequest = resolve
        }),
    ) as ApiClient['request']
    const pendingMount = await mountView(pending)
    expect(pendingMount.wrapper.text()).toContain('Loading model prices')
    resolveRequest(priceReport([], []))
    await flushPromises()
    expect(pendingMount.wrapper.text()).toContain('No built-in model prices')
    expect(pendingMount.wrapper.text()).toContain('No overrides configured')
    pendingMount.wrapper.unmount()

    const failed = vi.fn().mockRejectedValue(new Error('PRICE_ERROR_CANARY'))
    const failedMount = await mountView(failed as ApiClient['request'])
    expect(failedMount.wrapper.text()).toContain('Unable to load model prices')
    expect(failedMount.wrapper.text()).not.toContain('PRICE_ERROR_CANARY')
    failedMount.wrapper.unmount()
  })

  it('retains the last successful table and marks it stale when refresh fails', async () => {
    let calls = 0
    const request = vi.fn(async () => {
      calls += 1
      if (calls === 1) return priceReport()
      throw new Error('STALE_PRICE_CANARY')
    }) as ApiClient['request']
    const { queryClient: client, wrapper } = await mountView(request)

    await client.invalidateQueries({ queryKey: controlQueryKeys.modelPrices() })
    await flushPromises()

    expect(wrapper.get('[data-test="builtin-price-row-0"]')).toBeDefined()
    expect(wrapper.text()).toContain('may be stale')
    expect(wrapper.text()).not.toContain('STALE_PRICE_CANARY')
    wrapper.unmount()
  })

  it('deletes only the selected exact override when broader user prefix and global rules remain', async () => {
    const request = vi.fn(async (path: string, options?: ApiRequestOptions) => {
      if (path === '/api/model-prices' && options?.method === 'GET') {
        return priceReport([builtin], [exactOverride, override, globalOverride])
      }
      if (path === '/api/model-prices?pattern=vendor-model' && options?.method === 'DELETE') {
        return undefined
      }
      throw new Error(`unexpected ${path}`)
    }) as ApiClient['request']
    const { wrapper } = await mountView(request)

    await wrapper.findAll('[data-test="model-price-reset-open"]')[0]?.trigger('click')
    await flushPromises()
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(
      'the next applicable user rule is used first',
    )
    document.querySelector<HTMLButtonElement>('[data-test="model-price-reset-confirm"]')?.click()
    await flushPromises()

    expect(request).toHaveBeenCalledWith('/api/model-prices?pattern=vendor-model', {
      method: 'DELETE',
      signal: expect.any(AbortSignal),
    })
    expect(request).not.toHaveBeenCalledWith(
      '/api/model-prices?pattern=vendor-%2A',
      expect.anything(),
    )
    expect(request).not.toHaveBeenCalledWith('/api/model-prices?pattern=%2A', expect.anything())
    wrapper.unmount()
  })
})
