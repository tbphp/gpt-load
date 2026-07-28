import { mount } from '@vue/test-utils'

import type { AccessKeyOptionDto } from '@/api/control/types'
import AppSelect from '@/components/ui/AppSelect.vue'
import { createTestAppI18n } from '@/test/i18n'

import ConnectionCard from './ConnectionCard.vue'

function mountCard() {
  const appI18n = createTestAppI18n(undefined, 'en-US')
  return mount(ConnectionCard, {
    props: {
      origin: 'https://gateway.example.com',
      keys: [
        {
          id: 7,
          name: 'Production metadata',
          status: 'active',
          key: 'ACCESS_KEY_CANARY',
        } as AccessKeyOptionDto & { key: string },
      ],
      modelIds: ['gpt-4o', 'claude-sonnet-4', 'gemini-2.5-flash'],
    },
    global: {
      plugins: [appI18n.plugin],
      stubs: {
        RouterLink: {
          props: ['to'],
          template: '<a :href="to"><slot /></a>',
        },
      },
    },
  })
}

describe('ConnectionCard', () => {
  it('requires an explicit model choice and never renders AccessKey plaintext', async () => {
    const wrapper = mountCard()

    expect(wrapper.get('[data-test="connection-snippet"] code').text()).toContain('<MODEL_ID>')
    expect(wrapper.html()).not.toContain('ACCESS_KEY_CANARY')

    await wrapper.get('[data-test="connection-model"]').setValue('claude-sonnet-4')
    const protocolSelect = wrapper
      .findAllComponents(AppSelect)
      .find((select) => select.props('label') === 'Protocol')
    if (!protocolSelect) throw new Error('missing protocol select')
    protocolSelect.vm.$emit('update:modelValue', 'anthropic')
    await wrapper.vm.$nextTick()

    expect(wrapper.get('[data-test="connection-snippet"] code').text()).toContain('/v1/messages')
    expect(wrapper.get('[data-test="connection-snippet"] code').text()).toContain(
      'x-api-key: $GPT_LOAD_API_KEY',
    )
    expect(wrapper.get('[data-test="connection-snippet"] code').text()).toContain(
      'anthropic-version:',
    )
  })

  it('lets the user explicitly collapse and restore Connection Setup', async () => {
    const wrapper = mountCard()

    expect(wrapper.find('[data-test="connection-content"]').exists()).toBe(true)
    await wrapper.get('[data-test="connection-toggle"]').trigger('click')
    expect(wrapper.find('[data-test="connection-content"]').exists()).toBe(false)
    await wrapper.get('[data-test="connection-toggle"]').trigger('click')
    expect(wrapper.find('[data-test="connection-content"]').exists()).toBe(true)
  })
})
