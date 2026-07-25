import { mount } from '@vue/test-utils'
import { defineComponent, nextTick, ref } from 'vue'

import { createAppI18n } from '@/i18n'

import HeaderRulesEditor from './HeaderRulesEditor.vue'

function mountEditor(initial = { set: {}, remove: [] as string[] }, disabled = false) {
  const Host = defineComponent({
    components: { HeaderRulesEditor },
    setup() {
      return { rules: ref(initial), valid: ref(true), disabled }
    },
    template: '<HeaderRulesEditor v-model="rules" v-model:valid="valid" :disabled="disabled" />',
  })
  return mount(Host, {
    attachTo: document.body,
    global: { plugins: [createAppI18n(undefined, 'en-US').plugin] },
  })
}

describe('HeaderRulesEditor', () => {
  it('emits structured Set and Remove rules and masks every Set value by default', async () => {
    const wrapper = mountEditor()
    await wrapper.get('[data-test="add-header-rule"]').trigger('click')
    await wrapper.get('[data-test="header-name"]').setValue('X-Token')
    const value = wrapper.get('[data-test="header-value"]')
    expect(value.attributes('type')).toBe('password')
    await value.setValue('HEADER_CANARY_57aa')

    await wrapper.get('[data-test="add-header-rule"]').trigger('click')
    const actions = wrapper.findAll('[data-test="header-action"]')
    await actions[1]!.setValue('remove')
    const names = wrapper.findAll('[data-test="header-name"]')
    await names[1]!.setValue('X-Debug')

    expect((wrapper.vm as unknown as { rules: unknown }).rules).toEqual({
      set: { 'X-Token': 'HEADER_CANARY_57aa' },
      remove: ['X-Debug'],
    })
    expect(wrapper.text()).not.toContain('HEADER_CANARY_57aa')
    wrapper.unmount()
  })

  it('disables every mutable HeaderRules subcontrol', () => {
    const wrapper = mountEditor({ set: { 'X-Test': 'secret' }, remove: [] }, true)
    for (const selector of [
      '[data-test="add-header-rule"]',
      '[data-test="header-action"]',
      '[data-test="header-name"]',
      '[data-test="header-value"]',
      '[data-test="toggle-header-value"]',
      '[data-test="delete-header-rule"]',
    ]) {
      expect(wrapper.get(selector).attributes()).toHaveProperty('disabled')
    }
    wrapper.unmount()
  })

  it('reveals a Set value only by explicit action and reports case-insensitive duplicate names', async () => {
    const wrapper = mountEditor({ set: { 'X-Test': 'secret', 'x-test': 'other' }, remove: [] })
    expect(wrapper.findAll('[data-test="header-value"]')[0]!.attributes('type')).toBe('password')
    await wrapper.findAll('[data-test="toggle-header-value"]')[0]!.trigger('click')
    expect(wrapper.findAll('[data-test="header-value"]')[0]!.attributes('type')).toBe('text')
    expect(wrapper.get('[role="alert"]').text()).toContain('duplicate')
    wrapper.unmount()
  })

  it('normalizes ASCII header duplicates without locale-sensitive casing', () => {
    const localeLower = vi
      .spyOn(String.prototype, 'toLocaleLowerCase')
      .mockImplementation(function (this: string) {
        return this === 'I-Test' ? 'ı-test' : this.toLowerCase()
      })

    const wrapper = mountEditor({ set: { 'I-Test': 'secret', 'i-test': 'other' }, remove: [] })

    expect(wrapper.get('[role="alert"]').text()).toContain('duplicate')
    localeLower.mockRestore()
    wrapper.unmount()
  })

  it('publishes duplicate-name validity for section-level save blocking', async () => {
    const wrapper = mountEditor({ set: { 'X-Test': 'secret', 'x-test': 'other' }, remove: [] })

    expect((wrapper.vm as unknown as { valid: boolean }).valid).toBe(false)
    await wrapper.findAll('[data-test="header-name"]')[1]!.setValue('X-Other')
    expect((wrapper.vm as unknown as { valid: boolean }).valid).toBe(true)
    wrapper.unmount()
  })

  it('reconciles an external model replacement and resets every Set value to masked', async () => {
    const wrapper = mountEditor({ set: { 'X-Old': 'OLD_VALUE_CANARY' }, remove: ['X-Legacy'] })
    await wrapper.get('[data-test="toggle-header-value"]').trigger('click')
    expect(wrapper.get('[data-test="header-value"]').attributes('type')).toBe('text')

    ;(wrapper.vm as unknown as { rules: unknown }).rules = {
      set: { 'X-New': 'NEW_VALUE_CANARY' },
      remove: ['X-Remove'],
    }
    await nextTick()

    expect(
      wrapper
        .findAll('[data-test="header-name"]')
        .map((input) => (input.element as HTMLInputElement).value),
    ).toEqual(['X-New', 'X-Remove'])
    expect(
      wrapper
        .findAll('[data-test="header-action"]')
        .map((select) => (select.element as HTMLSelectElement).value),
    ).toEqual(['set', 'remove'])
    expect(wrapper.findAll('[data-test="header-value"]')).toHaveLength(1)
    expect((wrapper.get('[data-test="header-value"]').element as HTMLInputElement).value).toBe(
      'NEW_VALUE_CANARY',
    )
    expect(wrapper.get('[data-test="header-value"]').attributes('type')).toBe('password')
    expect(
      wrapper.findAll('input').map((input) => (input.element as HTMLInputElement).value),
    ).not.toContain('OLD_VALUE_CANARY')
    wrapper.unmount()
  })

  it('does not rebuild rows or lose focus when its own model update feeds back', async () => {
    const wrapper = mountEditor({ set: { 'X-Test': 'secret' }, remove: [] })
    const name = wrapper.get('[data-test="header-name"]')
    const originalElement = name.element
    ;(name.element as HTMLInputElement).focus()

    await name.setValue('X-Edited')
    await nextTick()

    expect(wrapper.get('[data-test="header-name"]').element).toBe(originalElement)
    expect(document.activeElement).toBe(originalElement)
    expect((wrapper.vm as unknown as { rules: unknown }).rules).toEqual({
      set: { 'X-Edited': 'secret' },
      remove: [],
    })
    wrapper.unmount()
  })

  it('keeps reveal and delete controls at least 44 by 44 CSS pixels', async () => {
    const wrapper = mountEditor({ set: { 'X-Test': 'secret' }, remove: [] })
    const reveal = wrapper.get('[data-test="toggle-header-value"]').element as HTMLButtonElement
    const remove = wrapper.get('[data-test="delete-header-rule"]').element as HTMLButtonElement

    expect(reveal.style.minWidth).toBe('44px')
    expect(reveal.style.minHeight).toBe('44px')
    expect(remove.style.minWidth).toBe('44px')
    expect(remove.style.minHeight).toBe('44px')
    wrapper.unmount()
  })
})
