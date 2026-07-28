import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import type { ApiClient } from '@/api/client'
import type { ModelPriceRuleDto } from '@/app/resources/model-prices'
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
  pricing_policy: {
    input_threshold_tokens: 272000,
    input_multiplier: 2,
    output_multiplier: 1.5,
  },
}
const override: ModelPriceRuleDto = {
  ...builtin,
  pattern: 'vendor-*',
  source: 'user',
  source_url: null,
  pricing_policy: null,
}

function queryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

async function mountDrawer(
  request: ApiClient['request'],
  rule: ModelPriceRuleDto | null = null,
  locale: 'zh-CN' | 'en-US' | 'ja-JP' = 'en-US',
) {
  const client = queryClient()
  const mounted = await mountApp(ModelPriceDrawer, {
    api: { request },
    queryClient: client,
    locale,
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

function requestDrawerClose(path: 'cancel' | 'close' | 'escape' | 'outside'): void {
  if (path === 'cancel') {
    element<HTMLButtonElement>('.model-price-drawer__actions .app-button--secondary').click()
    return
  }
  if (path === 'close') {
    element<HTMLButtonElement>('.app-drawer__close').click()
    return
  }
  if (path === 'escape') {
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    return
  }
  const overlay = element<HTMLElement>('.app-drawer__overlay')
  overlay.dispatchEvent(new Event('pointerdown', { bubbles: true }))
  overlay.click()
}

describe('ModelPriceDrawer', () => {
  it('closes an untouched add draft without a discard prompt', async () => {
    const confirm = vi.fn(() => false)
    vi.stubGlobal('confirm', confirm)
    const { wrapper } = await mountDrawer(vi.fn() as ApiClient['request'])

    requestDrawerClose('cancel')
    await flushPromises()

    expect(confirm).not.toHaveBeenCalled()
    expect(wrapper.emitted('update:open')).toContainEqual([false])
    wrapper.unmount()
    vi.unstubAllGlobals()
  })

  it.each(['cancel', 'close', 'escape', 'outside'] as const)(
    'confirms a changed model price draft before %s close',
    async (path) => {
      const confirm = vi.fn(() => false)
      vi.stubGlobal('confirm', confirm)
      const { wrapper } = await mountDrawer(vi.fn() as ApiClient['request'])
      await setInput('[data-test="model-price-pattern"]', 'vendor-*')

      requestDrawerClose(path)
      await flushPromises()
      expect(wrapper.emitted('update:open') ?? []).not.toContainEqual([false])

      confirm.mockReturnValue(true)
      requestDrawerClose(path)
      await flushPromises()
      expect(wrapper.emitted('update:open')).toContainEqual([false])
      expect(confirm).toHaveBeenCalledTimes(2)
      wrapper.unmount()
      vi.unstubAllGlobals()
    },
  )

  it('adds a complete five-price override and invalidates only modelPrices', async () => {
    const confirm = vi.fn(() => false)
    vi.stubGlobal('confirm', confirm)
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
      { queryKey: controlQueryKeys.modelPrices(), exact: true },
    ])
    expect(client.getMutationCache().getAll()).toHaveLength(0)
    expect(wrapper.emitted('update:open')).toContainEqual([false])
    expect(confirm).not.toHaveBeenCalled()
    wrapper.unmount()
    vi.unstubAllGlobals()
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

  it('separates a clean built-in projection from its available override action', async () => {
    const confirm = vi.fn(() => false)
    vi.stubGlobal('confirm', confirm)
    const { wrapper } = await mountDrawer(vi.fn() as ApiClient['request'], builtin)

    expect(element<HTMLButtonElement>('[data-test="model-price-save"]').disabled).toBe(false)
    requestDrawerClose('close')
    await flushPromises()

    expect(confirm).not.toHaveBeenCalled()
    expect(wrapper.emitted('update:open')).toContainEqual([false])
    wrapper.unmount()
    vi.unstubAllGlobals()
  })

  it('requires an inline confirmation in the same Drawer before saving a bare global star', async () => {
    const request = vi.fn().mockResolvedValue(undefined) as ApiClient['request']
    const { wrapper } = await mountDrawer(request)

    await setInput('[data-test="model-price-pattern"]', '*')
    await setInput('[data-test="model-price-output"]', '8')
    expect(document.body.textContent).toContain(
      'All user rules take precedence over built-in rules',
    )
    expect(document.body.textContent).toContain('* shadows every built-in price rule')
    expect(document.querySelector('[data-test="model-price-global-confirm"]')).toBeNull()
    const save = element<HTMLButtonElement>('[data-test="model-price-save"]')
    expect(save.disabled).toBe(false)
    save.click()
    await flushPromises()
    expect(request).not.toHaveBeenCalled()

    const drawer = element<HTMLElement>('.app-drawer__content')
    const confirmation = element<HTMLElement>('[data-test="model-price-global-confirm"]')
    expect(document.querySelectorAll('[role="dialog"]')).toHaveLength(1)
    expect(drawer.contains(confirmation)).toBe(true)
    expect(confirmation.textContent).toContain('Create a global user price override?')
    expect(confirmation.textContent).toContain(
      'takes precedence over every built-in exact and prefix rule',
    )
    expect(confirmation.textContent).toContain('Unset price slots do not fall back')
    expect(confirmation.textContent).toContain('future completed or Emit requests')
    expect(confirmation.textContent).toContain(
      'Reset restores the remaining user and built-in rules',
    )
    expect(document.activeElement).toBe(
      element<HTMLElement>('[data-test="model-price-global-confirm-heading"]'),
    )

    element<HTMLButtonElement>('[data-test="model-price-global-save-confirm"]').click()
    await flushPromises()
    expect(request).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it.each([
    ['zh-CN', '所有用户规则都优先于内置规则', '* 会遮蔽全部内置价格规则', '创建全局用户价格覆盖？'],
    [
      'en-US',
      'All user rules take precedence over built-in rules',
      '* shadows every built-in price rule',
      'Create a global user price override?',
    ],
    [
      'ja-JP',
      'すべてのユーザールールは組み込みルールより優先されます',
      '* はすべての組み込み価格ルールを覆い隠します',
      'グローバルユーザー価格オーバーライドを作成しますか？',
    ],
  ] as const)(
    'makes user precedence and complete builtin shadowing explicit before the dialog in %s',
    async (locale, precedence, shadowing, dialogTitle) => {
      const { wrapper } = await mountDrawer(vi.fn() as ApiClient['request'], null, locale)

      await setInput('[data-test="model-price-pattern"]', '*')
      await setInput('[data-test="model-price-output"]', '8')
      expect(document.body.textContent).toContain(precedence)
      expect(document.body.textContent).toContain(shadowing)
      element<HTMLButtonElement>('[data-test="model-price-save"]').click()
      await flushPromises()
      expect(
        element<HTMLElement>('[data-test="model-price-global-confirm"]').textContent,
      ).toContain(dialogTitle)

      wrapper.unmount()
    },
  )

  it('retains input and blocks every close path while a save is pending', async () => {
    let resolveRequest!: () => void
    const requestMock = vi.fn(
      (_path, options) =>
        new Promise<void>((resolve) => {
          resolveRequest = resolve
          void options
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

    for (const path of ['cancel', 'close', 'escape', 'outside'] as const) {
      requestDrawerClose(path)
    }
    await flushPromises()
    expect(signal?.aborted).toBe(false)
    expect(wrapper.emitted('update:open') ?? []).not.toContainEqual([false])

    resolveRequest()
    await flushPromises()
    expect(invalidate).toHaveBeenCalledOnce()
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
