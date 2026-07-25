import { mount } from '@vue/test-utils'

import AppSelect from './AppSelect.vue'

describe('AppSelect', () => {
  it('forwards control accessibility attributes to the combobox trigger', () => {
    const wrapper = mount(AppSelect, {
      props: {
        id: 'protocol',
        label: 'Protocol',
        modelValue: 'openai',
        options: [{ value: 'openai', label: 'OpenAI' }],
        'aria-describedby': 'protocol-help protocol-error',
        'aria-invalid': 'true',
      },
    })

    const combobox = wrapper.get('[role="combobox"]')
    expect(combobox.attributes('id')).toBe('protocol')
    expect(combobox.attributes('aria-describedby')).toBe('protocol-help protocol-error')
    expect(combobox.attributes('aria-invalid')).toBe('true')
  })
})
