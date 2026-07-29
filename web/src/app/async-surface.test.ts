import { flushPromises, mount } from '@vue/test-utils'
import { h, type Component } from 'vue'

import { createTestAppI18n } from '@/test/i18n'

import { lazySurface } from './async-surface'

describe('lazySurface', () => {
  it('renders a stable loading state before the child resolves', async () => {
    let resolve!: (component: Component) => void
    const component = lazySurface(
      () =>
        new Promise((next) => {
          resolve = next
        }),
    )
    const wrapper = mount(
      {
        render: () => h(component),
      },
      {
        global: { plugins: [createTestAppI18n().plugin] },
      },
    )

    expect(wrapper.get('[data-test="async-surface-loading"]').attributes('role')).toBe('status')
    resolve({ render: () => h('div', { 'data-test': 'loaded' }) })
    await flushPromises()
    await vi.waitFor(() => expect(wrapper.find('[data-test="loaded"]').exists()).toBe(true))
  })

  it('retries two failed loader attempts before rendering the child', async () => {
    const loader = vi
      .fn<() => Promise<Component>>()
      .mockRejectedValueOnce(new Error('first chunk failure'))
      .mockRejectedValueOnce(new Error('second chunk failure'))
      .mockResolvedValue({ render: () => h('div', { 'data-test': 'loaded' }) })
    const wrapper = mount(
      {
        render: () => h(lazySurface(loader)),
      },
      {
        global: {
          plugins: [createTestAppI18n().plugin],
          config: { errorHandler: () => undefined },
        },
      },
    )

    await flushPromises()
    await vi.waitFor(() => expect(wrapper.find('[data-test="loaded"]').exists()).toBe(true))
    expect(loader).toHaveBeenCalledTimes(3)
    expect(wrapper.find('[data-test="async-surface-error"]').exists()).toBe(false)
  })

  it('renders a reload recovery action when the child fails', async () => {
    const reload = vi.fn()
    const loader = vi.fn(() => Promise.reject(new Error('chunk failed')))
    const component = lazySurface(loader)
    const wrapper = mount(
      {
        render: () => h(component),
      },
      {
        global: {
          plugins: [createTestAppI18n().plugin],
          provide: { asyncSurfaceReload: reload },
          config: { errorHandler: () => undefined },
        },
      },
    )
    await flushPromises()
    await vi.waitFor(() =>
      expect(wrapper.find('[data-test="async-surface-error"]').exists()).toBe(true),
    )
    expect(loader).toHaveBeenCalledTimes(3)

    await wrapper.get('[data-test="async-surface-error"] button').trigger('click')
    expect(reload).toHaveBeenCalledTimes(1)
  })
})
