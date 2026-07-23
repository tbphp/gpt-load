import { mount } from '@vue/test-utils'
import { h } from 'vue'

import FormField from './FormField.vue'

describe('FormField', () => {
  it('associates label, control, description and error ids', () => {
    const wrapper = mount(FormField, {
      props: {
        id: 'auth-key',
        label: 'AUTH_KEY',
        description: 'Use the management credential.',
        error: 'The credential is required.',
      },
      slots: {
        default: ({ describedBy }: { describedBy?: string }) =>
          h('input', {
            id: 'auth-key',
            'aria-describedby': describedBy,
          }),
      },
    })

    expect(wrapper.get('label').attributes('for')).toBe('auth-key')
    expect(wrapper.get('#auth-key-description').text()).toBe('Use the management credential.')
    expect(wrapper.get('#auth-key-error').text()).toContain('The credential is required.')
    expect(wrapper.get('input').attributes('aria-describedby')).toBe(
      'auth-key-description auth-key-error',
    )
    expect(wrapper.get('#auth-key-error [aria-hidden="true"]').text()).toBe('▲')
  })
})
