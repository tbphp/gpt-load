import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'

import DataTable from './DataTable.vue'
import dataTableSource from './DataTable.vue?raw'

const observedElements: Element[][] = []
const resizeCallbacks: ResizeObserverCallback[] = []

beforeEach(() => {
  observedElements.length = 0
  resizeCallbacks.length = 0
  vi.stubGlobal(
    'ResizeObserver',
    class {
      private readonly index: number

      constructor(callback: ResizeObserverCallback) {
        this.index = resizeCallbacks.push(callback) - 1
        observedElements[this.index] = []
      }
      observe(element: Element) {
        observedElements[this.index]?.push(element)
      }
      disconnect() {}
      unobserve() {}
    },
  )
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('DataTable', () => {
  function notifyResize(): void {
    for (const callback of resizeCallbacks) {
      callback([], {} as ResizeObserver)
    }
  }

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

    notifyResize()
    await nextTick()

    expect(container.attributes('tabindex')).toBe('0')
    expect(container.attributes('aria-label')).toBe('Request logs')
    const scrollHintId = container.attributes('aria-describedby')
    expect(scrollHintId).toMatch(/^data-table-.+-scroll-hint$/)
    expect(wrapper.get(`#${scrollHintId}`).text()).toContain('Scroll horizontally')

    Object.defineProperty(container.element, 'scrollWidth', { configurable: true, value: 320 })
    notifyResize()
    await nextTick()

    expect(container.attributes('tabindex')).toBeUndefined()
  })

  it('uses an instance-unique hint id for non-Latin captions', () => {
    const wrapper = mount({
      components: { DataTable },
      template: `
        <div>
          <DataTable caption="用户覆盖价格" scroll-hint="横向滚动">
            <tbody><tr><td>first</td></tr></tbody>
          </DataTable>
          <DataTable caption="内置价格" scroll-hint="横向滚动">
            <tbody><tr><td>second</td></tr></tbody>
          </DataTable>
        </div>
      `,
    })

    const [firstId, secondId] = wrapper.findAll('.sr-only[id]').map((hint) => hint.attributes('id'))
    expect(firstId).toMatch(/^data-table-.+-scroll-hint$/)
    expect(secondId).toMatch(/^data-table-.+-scroll-hint$/)
    expect(firstId).not.toBe(secondId)
  })

  it('observes both the scroll container and table content width', () => {
    const wrapper = mount(DataTable, {
      props: { caption: 'Dynamic table' },
      slots: { default: '<tbody><tr><td>value</td></tr></tbody>' },
    })

    expect(observedElements).toHaveLength(1)
    expect(observedElements[0]).toEqual([
      wrapper.get('[data-table-scroll]').element,
      wrapper.get('table').element,
    ])
  })

  it('hides only explicitly low-priority columns below the compact breakpoint', () => {
    expect(dataTableSource).toMatch(
      /@media \(max-width: 759px\)\s*\{[\s\S]*\[data-column-priority='low'\][\s\S]*display: none;/,
    )
    expect(dataTableSource).not.toMatch(
      /\[data-column-priority='high'\][^{]*\{[^}]*display:\s*none;/,
    )
  })
})
