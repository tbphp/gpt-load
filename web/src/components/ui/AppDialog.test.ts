import { flushPromises, mount } from '@vue/test-utils'

import AppDialog from './AppDialog.vue'

async function mountDialog(dismissible?: boolean) {
  const wrapper = mount(AppDialog, {
    attachTo: document.body,
    props: {
      open: true,
      title: 'Confirm action',
      description: 'Review this action.',
      closeLabel: 'Close confirmation',
      dismissible,
    },
    slots: { default: '<button type="button">Continue</button>' },
  })
  await flushPromises()
  return wrapper
}

function element<T extends Element>(selector: string): T {
  const found = document.querySelector<T>(selector)
  if (!found) throw new Error(`missing ${selector}`)
  return found
}

describe('AppDialog', () => {
  it('allows the default close affordance', async () => {
    const wrapper = await mountDialog()

    element<HTMLButtonElement>('.app-dialog__close').click()
    await flushPromises()

    expect(wrapper.emitted('update:open')).toContainEqual([false])
    wrapper.unmount()
  })

  it('blocks X, Escape, and outside interaction when not dismissible', async () => {
    const wrapper = await mountDialog(false)
    const close = element<HTMLButtonElement>('.app-dialog__close')

    expect(close.disabled).toBe(true)
    close.click()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    const overlay = element<HTMLElement>('.app-dialog__overlay')
    overlay.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    overlay.click()
    await flushPromises()

    expect(wrapper.emitted('update:open') ?? []).not.toContainEqual([false])
    wrapper.unmount()
  })
})
