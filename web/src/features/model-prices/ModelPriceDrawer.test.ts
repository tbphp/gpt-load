import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import type { ApiClient } from '@/api/client'
import type { ModelPriceRuleDto } from '@/api/control/model-prices'
import { controlQueryKeys } from '@/app/query-keys'
import { mountApp } from '@/test/mount-app'

import ModelPriceDrawer from './ModelPriceDrawer.vue'

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
}
const override: ModelPriceRuleDto = {
  ...builtin,
  pattern: 'vendor-*',
  source: 'user',
  source_url: null,
}

function queryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

async function mountDrawer(
  request: ApiClient['request'],
  rule: ModelPriceRuleDto | null = null,
) {
  const client = queryClient()
  const mounted = await mountApp(ModelPriceDrawer, {
    api: { request },
    queryClient: client,
    locale: 'en-US',
    mounting: {
      props: { open: true, rule },
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

async function setInput(selector: string, value: string): Promise<void> {
  const input = element<HTMLInputElement>(selector)
  input.value = value
  input.dispatchEvent(new Event('input', { bubbles: true }))
  await flushPromises()
}

describe('ModelPriceDrawer', () => {
  it('adds a complete five-price override and invalidates only modelPrices', async () => {
    const request = vi.fn().mockResolvedValue(undefined) as ApiClient['request']
    const { queryClient: client, wrapper } = await mountDrawer(request)
    const invalidate = vi.spyOn(client, 'invalidateQueries').mockResolvedValue()
    const pattern = element<HTMLInputElement>('[data-test="model-price-pattern"]')
    expect(document.activeElement).toBe(pattern)
    expect(pattern.readOnly).toBe(false)

    await setInput('[data-test="model-price-pattern"]', 'vendor-*')
    await setInput('[data-test="model-price-output"]', '0')
    element<HTMLButtonElement>('[data-test="model-price-save"]').click()
    await flushPromises()

    expect(request).toHaveBeenCalledWith('/api/model-prices', {
      method: 'PUT',
      json: {
        pattern: 'vendor-*',
        prices: {
          uncached_input: null,
          cache_read: null,
          cache_write_5m: null,
          cache_write_1h: null,
          output: 0,
        },
      },
      signal: expect.any(AbortSignal),
    })
    expect(invalidate.mock.calls.map(([filters]) => filters)).toEqual([
      { queryKey: controlQueryKeys.modelPrices() },
    ])
    expect(client.getMutationCache().getAll()).toHaveLength(0)
    expect(wrapper.emitted('update:open')).toContainEqual([false])
    wrapper.unmount()
  })

  it.each([
    ['builtin', builtin, 'Create override from built-in price'],
    ['user', override, 'Edit model price override'],
  ])('prefills a %s rule and keeps its pattern readonly', async (_name, rule, title) => {
    const { wrapper } = await mountDrawer(vi.fn() as ApiClient['request'], rule)

    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(title)
    expect(element<HTMLInputElement>('[data-test="model-price-pattern"]').value).toBe(rule.pattern)
    expect(element<HTMLInputElement>('[data-test="model-price-pattern"]').readOnly).toBe(true)
    expect(element<HTMLInputElement>('[data-test="model-price-cache_read"]').value).toBe('0')
    expect(element<HTMLInputElement>('[data-test="model-price-cache_write_5m"]').value).toBe('')
    wrapper.unmount()
  })

  it('requires an explicit hard confirmation before saving a bare global star', async () => {
    const request = vi.fn().mockResolvedValue(undefined) as ApiClient['request']
    const { wrapper } = await mountDrawer(request)

    await setInput('[data-test="model-price-pattern"]', '*')
    await setInput('[data-test="model-price-output"]', '8')
    expect(document.body.textContent).toContain('matches every model')
    expect(element<HTMLButtonElement>('[data-test="model-price-save"]').disabled).toBe(true)

    const confirmation = element<HTMLInputElement>('[data-test="model-price-global-confirm"]')
    confirmation.checked = true
    confirmation.dispatchEvent(new Event('change', { bubbles: true }))
    await flushPromises()
    expect(element<HTMLButtonElement>('[data-test="model-price-save"]').disabled).toBe(false)
    element<HTMLButtonElement>('[data-test="model-price-save"]').click()
    await flushPromises()
    expect(request).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('retains input after a generic failure and aborts an in-flight save when closed', async () => {
    let rejectRequest!: (error: unknown) => void
    const requestMock = vi.fn(
      (_path, options) =>
        new Promise<void>((_resolve, reject) => {
          rejectRequest = reject
          options?.signal?.addEventListener('abort', () => reject(new DOMException('x', 'AbortError')))
        }),
    )
    const request = requestMock as ApiClient['request']
    const { queryClient: client, wrapper } = await mountDrawer(request)
    const invalidate = vi.spyOn(client, 'invalidateQueries').mockResolvedValue()

    await setInput('[data-test="model-price-pattern"]', 'vendor-model')
    await setInput('[data-test="model-price-output"]', '9')
    element<HTMLButtonElement>('[data-test="model-price-save"]').click()
    await flushPromises()
    expect(element<HTMLButtonElement>('[data-test="model-price-save"]').disabled).toBe(true)
    const signal = requestMock.mock.calls[0]?.[1]?.signal

    document.querySelector<HTMLButtonElement>('[aria-label="Close model price editor"]')?.click()
    await flushPromises()
    expect(signal?.aborted).toBe(true)
    rejectRequest(new Error('PRICE_SAVE_CANARY'))
    await flushPromises()
    expect(invalidate).not.toHaveBeenCalled()
    expect(document.body.textContent).not.toContain('PRICE_SAVE_CANARY')
    wrapper.unmount()
  })

  it('shows a localized generic save error without clearing valid input', async () => {
    const request = vi.fn().mockRejectedValue(new Error('PRICE_SAVE_CANARY'))
    const { wrapper } = await mountDrawer(request as ApiClient['request'])

    await setInput('[data-test="model-price-pattern"]', 'vendor-model')
    await setInput('[data-test="model-price-output"]', '9')
    element<HTMLButtonElement>('[data-test="model-price-save"]').click()
    await flushPromises()

    expect(document.body.textContent).toContain('Unable to save the model price override')
    expect(document.body.textContent).not.toContain('PRICE_SAVE_CANARY')
    expect(element<HTMLInputElement>('[data-test="model-price-output"]').value).toBe('9')
    wrapper.unmount()
  })
})
