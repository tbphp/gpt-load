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
          protocols: ['openai'],
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
})
