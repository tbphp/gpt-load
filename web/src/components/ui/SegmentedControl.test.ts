import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'

import SegmentedControl from './SegmentedControl.vue'

const options = [
  { value: '24h', label: '24h' },
  { value: '30d', label: '30d' },
  { value: '90d', label: '90d' },
]

describe('SegmentedControl', () => {
  it('exposes the selected segment and emits one semantic click change', async () => {
    const wrapper = mount(SegmentedControl, {
      props: { modelValue: '24h', label: 'Range', options },
    })

    expect(wrapper.get('[data-segment-value="24h"]').attributes('aria-selected')).toBe('true')
    await wrapper.get('[data-segment-value="30d"]').trigger('mousedown', {
      button: 0,
      ctrlKey: false,
    })
    await wrapper.get('[data-segment-value="30d"]').trigger('click')

    expect(wrapper.emitted('update:modelValue')).toEqual([['30d']])
  })

  it.each([
    ['ArrowRight', '30d'],
    ['End', '90d'],
  ] as const)('supports %s keyboard navigation', async (key, expected) => {
    const wrapper = mount(SegmentedControl, {
      attachTo: document.body,
      props: { modelValue: '24h', label: 'Range', options },
    })
    const current = wrapper.get<HTMLElement>('[data-segment-value="24h"]')
    current.element.focus()

    await current.trigger('keydown', { key })
    await nextTick()

    expect(wrapper.emitted('update:modelValue')).toEqual([[expected]])
    wrapper.unmount()
  })

  it('supports ArrowLeft and Home from the last segment', async () => {
    const wrapper = mount(SegmentedControl, {
      attachTo: document.body,
      props: { modelValue: '90d', label: 'Range', options },
    })
    const current = wrapper.get<HTMLElement>('[data-segment-value="90d"]')
    current.element.focus()

    await current.trigger('keydown', { key: 'ArrowLeft' })
    await nextTick()
    expect(wrapper.emitted('update:modelValue')).toEqual([['30d']])

    await wrapper.setProps({ modelValue: '90d' })
    current.element.focus()
    await current.trigger('keydown', { key: 'Home' })
    await nextTick()
    expect(wrapper.emitted('update:modelValue')).toEqual([['30d'], ['24h']])
    wrapper.unmount()
  })
})
