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

  it('exposes required, invalid and disabled-reason metadata to the control slot', () => {
    const wrapper = mount(FormField, {
      props: {
        id: 'group-name',
        label: 'Name',
        required: true,
        requiredText: 'Required',
        error: 'Name is required.',
        disabledReason: 'Wait for discovery to finish.',
      },
      slots: {
        default: ({
          describedBy,
          invalid,
          required,
        }: {
          describedBy?: string
          invalid: boolean
          required: boolean
        }) =>
          h('input', {
            id: 'group-name',
            'aria-describedby': describedBy,
            'aria-invalid': invalid,
            required,
          }),
      },
    })

    expect(wrapper.get('label').text()).toContain('Required')
    expect(wrapper.get('input').attributes('required')).toBe('')
    expect(wrapper.get('input').attributes('aria-invalid')).toBe('true')
    expect(wrapper.get('input').attributes('aria-describedby')).toBe(
      'group-name-disabled-reason group-name-error',
    )
    expect(wrapper.get('#group-name-disabled-reason').text()).toBe('Wait for discovery to finish.')
  })
})
