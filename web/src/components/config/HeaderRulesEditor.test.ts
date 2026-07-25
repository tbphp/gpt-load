import { mount } from '@vue/test-utils'
import { defineComponent, ref } from 'vue'

import { createAppI18n } from '@/i18n'

import HeaderRulesEditor from './HeaderRulesEditor.vue'

function mountEditor(initial = { set: {}, remove: [] as string[] }) {
  const Host = defineComponent({
    components: { HeaderRulesEditor },
    setup() {
      return { rules: ref(initial) }
    },
    template: '<HeaderRulesEditor v-model="rules" />',
  })
  return mount(Host, { global: { plugins: [createAppI18n(undefined, 'en-US').plugin] } })
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
  })

  it('reveals a Set value only by explicit action and reports case-insensitive duplicate names', async () => {
    const wrapper = mountEditor({ set: { 'X-Test': 'secret', 'x-test': 'other' }, remove: [] })
    expect(wrapper.findAll('[data-test="header-value"]')[0]!.attributes('type')).toBe('password')
    await wrapper.findAll('[data-test="toggle-header-value"]')[0]!.trigger('click')
    expect(wrapper.findAll('[data-test="header-value"]')[0]!.attributes('type')).toBe('text')
    expect(wrapper.get('[role="alert"]').text()).toContain('duplicate')
  })
})
