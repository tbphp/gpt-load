import { mount } from '@vue/test-utils'

import AppTabs from './AppTabs.vue'

describe('AppTabs active trigger visibility', () => {
  it.each([375, 768, 1024])(
    'scrolls the active trigger into the nearest visible geometry at %dpx',
    async (viewportWidth) => {
      const scrollIntoView = vi.fn()
      Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', {
        configurable: true,
        value: scrollIntoView,
      })
      Object.defineProperty(document.documentElement, 'clientWidth', {
        configurable: true,
        value: viewportWidth,
      })
      const wrapper = mount(AppTabs, {
        props: {
          modelValue: 'overview',
          label: 'Sections',
          items: [
            { value: 'overview', label: 'Overview' },
            { value: 'health', label: 'Health' },
            { value: 'models', label: 'Models' },
            { value: 'long-running-operations', label: 'Long running operations' },
          ],
        },
        slots: { default: 'content' },
      })

      await wrapper.setProps({ modelValue: 'long-running-operations' })

      expect(
        wrapper.get('[data-tab-value="long-running-operations"]').attributes('data-state'),
      ).toBe('active')
      expect(scrollIntoView).toHaveBeenCalledTimes(1)
      expect(scrollIntoView).toHaveBeenCalledWith({
        block: 'nearest',
        inline: 'nearest',
      })
      wrapper.unmount()
    },
  )
})
