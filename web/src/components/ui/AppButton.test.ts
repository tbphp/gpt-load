import { mount } from '@vue/test-utils'

import AppButton from './AppButton.vue'

describe('AppButton', () => {
  it('forwards native type and disabled state', () => {
    const wrapper = mount(AppButton, {
      props: {
        type: 'reset',
        disabled: true,
      },
      slots: {
        default: 'Reset form',
      },
    })

    const button = wrapper.get('button')
    expect(button.attributes('type')).toBe('reset')
    expect(button.attributes()).toHaveProperty('disabled')
    expect(button.text()).toBe('Reset form')
  })

  it('exposes busy state without allowing a second click', async () => {
    const onClick = vi.fn()
    const wrapper = mount(AppButton, {
      props: {
        busy: true,
        onClick,
      },
      slots: {
        default: 'Verifying',
      },
    })

    const button = wrapper.get('button')
    expect(button.attributes('aria-busy')).toBe('true')
    expect(button.attributes()).toHaveProperty('disabled')

    await button.trigger('click')
    expect(onClick).not.toHaveBeenCalled()
  })
})
