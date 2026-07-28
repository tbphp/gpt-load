import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'

import DataTable from './DataTable.vue'

let notifyResize: (() => void) | undefined

beforeEach(() => {
  notifyResize = undefined
  vi.stubGlobal(
    'ResizeObserver',
    class {
      constructor(callback: ResizeObserverCallback) {
        notifyResize = () => callback([], this as unknown as ResizeObserver)
      }
      observe() {}
      disconnect() {}
      unobserve() {}
    },
  )
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('DataTable', () => {
  it('makes only an overflowing table region keyboard focusable and describes scrolling', async () => {
    const wrapper = mount(DataTable, {
      props: {
        caption: 'Request logs',
        scrollHint: 'Scroll horizontally to inspect every column.',
      },
      slots: {
        default: '<tbody><tr><td>request</td></tr></tbody>',
      },
    })
    const container = wrapper.get('[data-table-scroll]')
    Object.defineProperties(container.element, {
      clientWidth: { configurable: true, value: 320 },
      scrollWidth: { configurable: true, value: 720 },
    })

    notifyResize?.()
    await nextTick()

    expect(container.attributes('tabindex')).toBe('0')
    expect(container.attributes('aria-label')).toBe('Request logs')
    expect(container.attributes('aria-describedby')).toBe('request-logs-scroll-hint')
    expect(wrapper.get('#request-logs-scroll-hint').text()).toContain('Scroll horizontally')

    Object.defineProperty(container.element, 'scrollWidth', { configurable: true, value: 320 })
    notifyResize?.()
    await nextTick()

    expect(container.attributes('tabindex')).toBeUndefined()
  })
})
