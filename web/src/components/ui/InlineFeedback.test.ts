import { mount } from '@vue/test-utils'

import InlineFeedback from './InlineFeedback.vue'

describe('InlineFeedback', () => {
  it.each([
    ['info', 'status'],
    ['danger', 'alert'],
    ['warning', 'alert'],
  ] as const)('uses an icon, text and an accessible role for %s', (tone, role) => {
    const wrapper = mount(InlineFeedback, {
      props: { tone },
      slots: {
        default: `${tone} feedback`,
      },
    })

    expect(wrapper.attributes('role')).toBe(role)
    expect(wrapper.get('[aria-hidden="true"]').text()).not.toBe('')
    expect(wrapper.text()).toContain(`${tone} feedback`)
  })

  it('uses a triangular icon for warning feedback', () => {
    const wrapper = mount(InlineFeedback, {
      props: { tone: 'warning' },
    })

    expect(wrapper.get('[aria-hidden="true"]').text()).toBe('▲')
  })

  it('uses a triangular icon for danger feedback', () => {
    const wrapper = mount(InlineFeedback, {
      props: { tone: 'danger' },
    })

    expect(wrapper.get('[aria-hidden="true"]').text()).toBe('▲')
  })
})
