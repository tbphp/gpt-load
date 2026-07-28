import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'

import AppPopover from './AppPopover.vue'

describe('AppPopover', () => {
  it('closes on Escape and restores focus to its trigger', async () => {
    let wrapper!: VueWrapper
    wrapper = mount(AppPopover, {
      attachTo: document.body,
      props: {
        open: false,
        'onUpdate:open': (open: boolean) => wrapper.setProps({ open }),
      },
      slots: {
        trigger: '<button type="button">Preferences</button>',
        default: '<button type="button">Theme choice</button>',
      },
    })
    const trigger = wrapper.get('button')
    trigger.element.focus()
    await trigger.trigger('click')
    await flushPromises()

    expect(document.querySelector('.app-popover__content')).not.toBeNull()

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await flushPromises()

    expect(document.querySelector('.app-popover__content')).toBeNull()
    expect(document.activeElement).toBe(trigger.element)
    wrapper.unmount()
  })
})
