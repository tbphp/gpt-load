import { mount } from '@vue/test-utils'

import OperationNotice from './OperationNotice.vue'

describe('OperationNotice', () => {
  it('announces an indeterminate operation and emits its named recovery action', async () => {
    const wrapper = mount(OperationNotice, {
      props: {
        state: 'indeterminate',
        message: 'The server result is not confirmed.',
        actionLabel: 'Check result',
      },
    })

    expect(wrapper.attributes('role')).toBe('status')
    expect(wrapper.attributes('aria-live')).toBe('polite')
    expect(wrapper.text()).toContain('The server result is not confirmed.')

    await wrapper.get('button').trigger('click')
    expect(wrapper.emitted('recover')).toHaveLength(1)
  })

  it('disables recovery while reconciliation is running', () => {
    const wrapper = mount(OperationNotice, {
      props: {
        state: 'reconciling',
        message: 'Checking the server result.',
        actionLabel: 'Check result',
        busy: true,
      },
    })

    expect(wrapper.get('button').attributes()).toHaveProperty('disabled')
    expect(wrapper.get('button').attributes('aria-busy')).toBe('true')
  })
})
