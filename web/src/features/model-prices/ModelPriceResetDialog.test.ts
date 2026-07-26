import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import type { ApiClient, ApiRequestOptions } from '@/api/client'
import type { ModelPriceRuleDto } from '@/api/control/model-prices'
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
}

async function mountDialog(request: ApiClient['request']) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const mounted = await mountApp(ModelPriceResetDialog, {
    api: { request },
    queryClient: client,
    locale: 'en-US',
    mounting: { props: { rule: override }, attachTo: document.body },
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
      'Remove the complete override rule',
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

  it('disables duplicate confirmation and aborts an in-flight reset when closed', async () => {
    let signal: AbortSignal | null | undefined
    const requestMock = vi.fn(
      (path: string, options?: ApiRequestOptions) => {
        if (path !== '/api/model-prices?pattern=vendor-%2A') {
          throw new Error(`unexpected ${path}`)
        }
        signal = options?.signal
        return new Promise<void>(() => {
          // Remains pending until the dialog aborts the request.
        })
      },
    )
    const request = requestMock as ApiClient['request']
    const { wrapper } = await mountDialog(request)

    await wrapper.get('[data-test="model-price-reset-open"]').trigger('click')
    await flushPromises()
    button('[data-test="model-price-reset-confirm"]').click()
    button('[data-test="model-price-reset-confirm"]').click()
    await flushPromises()
    expect(request).toHaveBeenCalledTimes(1)
    expect(button('[data-test="model-price-reset-confirm"]').disabled).toBe(true)
    document
      .querySelector<HTMLButtonElement>('[aria-label="Close model price reset confirmation"]')
      ?.click()
    await flushPromises()
    expect(signal?.aborted).toBe(true)
    wrapper.unmount()
  })
})
