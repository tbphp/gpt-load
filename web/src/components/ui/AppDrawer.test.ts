import { flushPromises, mount } from '@vue/test-utils'

import AppDrawer from './AppDrawer.vue'

async function mountDrawer(dismissible?: boolean) {
  const wrapper = mount(AppDrawer, {
    attachTo: document.body,
    props: {
      open: true,
      title: 'Edit resource',
      description: 'Edit this resource.',
      closeLabel: 'Close editor',
      dismissible,
    },
    slots: { default: '<button type="button">Save</button>' },
  })
  await flushPromises()
  return wrapper
}

function element<T extends Element>(selector: string): T {
  const found = document.querySelector<T>(selector)
  if (!found) throw new Error(`missing ${selector}`)
  return found
}

describe('AppDrawer', () => {
  it('blocks X, Escape, and outside interaction when not dismissible', async () => {
    const wrapper = await mountDrawer(false)
    const close = element<HTMLButtonElement>('.app-drawer__close')

    expect(close.disabled).toBe(true)
    close.click()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    const overlay = element<HTMLElement>('.app-drawer__overlay')
    overlay.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    overlay.click()
    await flushPromises()

    expect(wrapper.emitted('update:open') ?? []).not.toContainEqual([false])
    wrapper.unmount()
  })
})
