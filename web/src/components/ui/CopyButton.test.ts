import { mount } from '@vue/test-utils'

import CopyButton from './CopyButton.vue'

function mountCopyButton() {
  return mount(CopyButton, {
    props: {
      value: 'literal-value',
      label: 'Copy value',
      successLabel: 'Copied',
      failureLabel: 'Copy failed',
    },
  })
}

describe('CopyButton', () => {
  it('announces successful copy without exposing the copied value', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    vi.stubGlobal('navigator', { clipboard: { writeText } })
    const wrapper = mountCopyButton()

    await wrapper.get('button').trigger('click')
    await Promise.resolve()

    expect(writeText).toHaveBeenCalledWith('literal-value')
    expect(wrapper.get('[role="status"]').text()).toBe('Copied')
    expect(wrapper.text()).not.toContain('literal-value')
  })

  it('announces clipboard failure', async () => {
    vi.stubGlobal('navigator', {
      clipboard: { writeText: vi.fn().mockRejectedValue(new DOMException('denied')) },
    })
    const wrapper = mountCopyButton()

    await wrapper.get('button').trigger('click')
    await Promise.resolve()

    expect(wrapper.get('[role="status"]').text()).toBe('Copy failed')
  })
})
