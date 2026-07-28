import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import type { ApiClient, ApiRequestOptions } from '@/api/client'
import type { ModelPriceRuleDto } from '@/app/resources/model-prices'
import { controlQueryKeys } from '@/app/query-keys'
import { mountApp } from '@/test/mount-app'

import ModelPriceResetDialog from './ModelPriceResetDialog.vue'

const override: ModelPriceRuleDto = {
  pattern: 'vendor-*',
  source: 'user',
  prices: {
    uncached_input: null,
    cache_read: 0,
    cache_write_5m: null,
    cache_write_1h: null,
    output: 8,
  },
  source_url: null,
  updated_at: '2026-07-27T00:00:00Z',
  pricing_policy: null,
}

async function mountDialog(
  request: ApiClient['request'],
  rule: ModelPriceRuleDto = override,
  locale: 'zh-CN' | 'en-US' | 'ja-JP' = 'en-US',
) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const mounted = await mountApp(ModelPriceResetDialog, {
    api: { request },
    queryClient: client,
    locale,
    mounting: { props: { rule }, attachTo: document.body },
  })
  await flushPromises()
  return { ...mounted, queryClient: client }
}

function button(selector: string): HTMLButtonElement {
  const found = document.querySelector<HTMLButtonElement>(selector)
  if (!found) throw new Error(`missing ${selector}`)
  return found
}

describe('ModelPriceResetDialog', () => {
  it('confirms reset, deletes the exact pattern, and invalidates only modelPrices', async () => {
    const request = vi.fn().mockResolvedValue(undefined) as ApiClient['request']
    const { queryClient: client, wrapper } = await mountDialog(request)
    const invalidate = vi.spyOn(client, 'invalidateQueries').mockResolvedValue()

    await wrapper.get('[data-test="model-price-reset-open"]').trigger('click')
    await flushPromises()
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(
      'the next applicable user rule is used first',
    )
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(
      'If no user or built-in rule matches, the model may be unpriced',
    )
    button('[data-test="model-price-reset-confirm"]').click()
    await flushPromises()

    expect(request).toHaveBeenCalledWith('/api/model-prices?pattern=vendor-%2A', {
      method: 'DELETE',
      signal: expect.any(AbortSignal),
    })
    expect(invalidate.mock.calls.map(([filters]) => filters)).toEqual([
      { queryKey: controlQueryKeys.modelPrices() },
    ])
    expect(wrapper.emitted('reset')).toEqual([[]])
    wrapper.unmount()
  })

  it.each([
    [
      'zh-CN',
      '删除后会先使用下一条适用的用户规则，再回退到内置规则',
      '如果用户规则和内置规则都不匹配，该模型可能没有价格',
    ],
    [
      'en-US',
      'After deletion, the next applicable user rule is used first, then a built-in rule',
      'If no user or built-in rule matches, the model may be unpriced',
    ],
    [
      'ja-JP',
      '削除後は次に適用可能なユーザールールを先に使い、その後に組み込みルールへフォールバックします',
      'ユーザールールにも組み込みルールにも一致しない場合、モデルは価格未設定になる可能性があります',
    ],
  ] as const)(
    'explains the user-to-builtin-to-unpriced fallback chain in %s',
    async (locale, userFallback, unpriced) => {
      const exactRule = { ...override, pattern: 'vendor-model' }
      const { wrapper } = await mountDialog(vi.fn() as ApiClient['request'], exactRule, locale)

      await wrapper.get('[data-test="model-price-reset-open"]').trigger('click')
      await flushPromises()
      expect(document.querySelector('[role="dialog"]')?.textContent).toContain(userFallback)
      expect(document.querySelector('[role="dialog"]')?.textContent).toContain(unpriced)

      wrapper.unmount()
    },
  )

  it('keeps the confirmation open on failure and never renders error details', async () => {
    const request = vi.fn().mockRejectedValue(new Error('RESET_PRICE_CANARY'))
    const { wrapper } = await mountDialog(request as ApiClient['request'])

    await wrapper.get('[data-test="model-price-reset-open"]').trigger('click')
    await flushPromises()
    button('[data-test="model-price-reset-confirm"]').click()
    await flushPromises()

    expect(document.querySelector('[role="dialog"]')).not.toBeNull()
    expect(document.body.textContent).toContain('Unable to reset the model price override')
    expect(document.body.textContent).not.toContain('RESET_PRICE_CANARY')
    wrapper.unmount()
  })

  it('disables duplicate confirmation and blocks dismissing an in-flight reset', async () => {
    let signal: AbortSignal | null | undefined
    let resolveReset!: () => void
    const requestMock = vi.fn((path: string, options?: ApiRequestOptions) => {
      if (path !== '/api/model-prices?pattern=vendor-%2A') {
        throw new Error(`unexpected ${path}`)
      }
      signal = options?.signal
      return new Promise<void>((resolve) => {
        resolveReset = resolve
      })
    })
    const request = requestMock as ApiClient['request']
    const { wrapper } = await mountDialog(request)

    await wrapper.get('[data-test="model-price-reset-open"]').trigger('click')
    await flushPromises()
    button('[data-test="model-price-reset-confirm"]').click()
    button('[data-test="model-price-reset-confirm"]').click()
    await flushPromises()
    expect(request).toHaveBeenCalledTimes(1)
    expect(button('[data-test="model-price-reset-confirm"]').disabled).toBe(true)
    const close = button('[aria-label="Close model price reset confirmation"]')
    expect(close.disabled).toBe(true)
    close.click()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    document.querySelector<HTMLElement>('.app-dialog__overlay')?.click()
    await flushPromises()
    expect(document.querySelector('[role="dialog"]')).not.toBeNull()
    expect(signal?.aborted).toBe(false)

    resolveReset()
    await flushPromises()
    wrapper.unmount()
  })
})
