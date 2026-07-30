import { QueryClient } from '@tanstack/vue-query'

import { mountApp } from '@/test/mount-app'

import GroupSettingsBaseForm from './GroupSettingsBaseForm.vue'

describe('GroupSettingsBaseForm', () => {
  it('emits draft intents without owning merge or save state', async () => {
    const { wrapper } = await mountApp(GroupSettingsBaseForm, {
      api: { request: vi.fn() },
      queryClient: new QueryClient(),
      mounting: {
        props: {
          name: 'Primary',
          upstreamUrl: 'https://example.com',
          validationModel: null,
          weightManual: null,
          protocols: ['openai-chat-completions'],
          enabled: true,
          pending: false,
          nameError: '',
          upstreamUrlError: '',
          protocolsError: '',
        },
      },
    })

    await wrapper.get('[data-test="group-name"]').setValue('Renamed')
    expect(wrapper.emitted('update:name')?.[0]).toEqual(['Renamed'])
  })

  it('offers all canonical protocols and warns when Responses is selected', async () => {
    const { wrapper } = await mountApp(GroupSettingsBaseForm, {
      api: { request: vi.fn() },
      queryClient: new QueryClient(),
      mounting: {
        props: {
          name: 'Responses only',
          upstreamUrl: 'https://api.example.com',
          validationModel: null,
          weightManual: null,
          protocols: ['openai-responses'],
          enabled: true,
          pending: false,
          nameError: '',
          upstreamUrlError: '',
          protocolsError: '',
        },
      },
    })

    expect(
      wrapper
        .findAll('[data-test^="group-protocol-"]')
        .map((option) => option.attributes('data-test')),
    ).toEqual([
      'group-protocol-openai-chat-completions',
      'group-protocol-openai-responses',
      'group-protocol-anthropic',
      'group-protocol-gemini',
    ])
    expect(
      (wrapper.get('[data-test="group-protocol-openai-responses"]').element as HTMLInputElement)
        .checked,
    ).toBe(true)
    expect(wrapper.get('[data-test="group-responses-affinity-warning"]').text()).toContain(
      '上游 Key',
    )
    expect(wrapper.get('[data-test="group-responses-usage-options-help"]').text()).toContain(
      'Responses 会忽略',
    )

    await wrapper.get('[data-test="group-protocol-openai-chat-completions"]').setValue(true)
    expect(wrapper.emitted('toggleProtocol')?.[0]).toEqual(['openai-chat-completions', true])
  })
})
