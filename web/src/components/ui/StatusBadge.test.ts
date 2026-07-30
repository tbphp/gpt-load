import { mount } from '@vue/test-utils'

import StatusBadge from './StatusBadge.vue'

describe('StatusBadge', () => {
  it.each(['neutral', 'warning', 'danger'] as const)(
    'renders %s state with both visible text and a decorative status icon',
    (tone) => {
      const wrapper = mount(StatusBadge, {
        props: { tone },
        slots: { default: `${tone} state` },
      })

      expect(wrapper.text()).toBe(`${tone} state`)
      expect(wrapper.classes()).toContain(`status-badge--${tone}`)
      expect(wrapper.get('svg').attributes('aria-hidden')).toBe('true')
    },
  )

  it('renders an unknown operational status as a neutral help state', () => {
    const wrapper = mount(StatusBadge, {
      props: { status: 'unknown' },
      slots: { default: 'Unknown' },
    })

    expect(wrapper.classes()).toContain('status-badge--neutral')
    expect(wrapper.get('svg').attributes('data-status-icon')).toBe('help')
  })
})
