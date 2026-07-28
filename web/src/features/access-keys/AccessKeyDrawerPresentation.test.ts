import { QueryClient } from '@tanstack/vue-query'

import { mountApp } from '@/test/mount-app'

import AccessKeyFormFields from './AccessKeyFormFields.vue'
import AccessKeyOperationFeedback from './AccessKeyOperationFeedback.vue'
import AccessKeyResultPanel from './AccessKeyResultPanel.vue'
import AccessKeyScopeEditor from './AccessKeyScopeEditor.vue'

const api = { request: vi.fn() }

describe('AccessKey drawer presentation boundaries', () => {
  it('emits form intents without owning the draft', async () => {
    const { wrapper } = await mountApp(AccessKeyFormFields, {
      api,
      queryClient: new QueryClient(),
      mounting: {
        props: {
          name: 'client',
          status: 'active',
          rpmLimit: 10,
          editing: true,
          disabled: false,
        },
      },
    })

    await wrapper.get('[data-test="access-key-name"]').setValue('renamed')
    await wrapper.get('[data-test="access-key-status"]').setValue('disabled')
    await wrapper.get('[data-test="access-key-rpm"]').setValue('20')

    expect(wrapper.emitted('update:name')?.[0]).toEqual(['renamed'])
    expect(wrapper.emitted('update:status')?.[0]).toEqual(['disabled'])
    expect(wrapper.emitted('update:rpmLimit')?.[0]).toEqual([20])
  })

  it('emits scope intents while preserving controlled values', async () => {
    const { wrapper } = await mountApp(AccessKeyScopeEditor, {
      api,
      queryClient: new QueryClient(),
      mounting: {
        props: {
          modes: { groups: 'restricted', protocols: 'all', models: 'restricted' },
          filters: { groups: [7], protocols: [], models: [] },
          groupOptions: [{ id: 7, label: 'Primary', dangling: false }],
          groupCatalogState: 'ready',
          protocolOptions: ['openai'],
          modelOptions: ['gpt-4.1'],
          modelInput: '',
          baseGroupIds: [7],
          disabled: false,
        },
      },
    })

    await wrapper.get('[data-test="access-key-groups-mode"]').setValue('all')
    await wrapper.get('input[type="checkbox"]').setValue(false)
    await wrapper.get('[data-test="access-key-model-input"]').setValue('gpt-4.1')
    await wrapper.setProps({ modelInput: 'gpt-4.1' })
    await wrapper.get('[data-test="access-key-model-add"]').trigger('click')

    expect(wrapper.emitted('setScopeMode')?.[0]).toEqual(['groups', 'all'])
    expect(wrapper.emitted('toggleGroup')?.[0]).toEqual([7, false])
    expect(wrapper.emitted('update:modelInput')?.[0]).toEqual(['gpt-4.1'])
    expect(wrapper.emitted('addModel')).toHaveLength(1)
  })

  it('emits reveal intent and renders operation feedback', async () => {
    const result = {
      id: 9,
      name: 'client',
      masked_key: 'sk-gl-••••••••cafe',
      status: 'active' as const,
      filters: { groups: [], protocols: [], models: [] },
      rpm_limit: 0,
      created_at: '2026-07-29T00:00:00Z',
      updated_at: '2026-07-29T00:00:00Z',
    }
    const { wrapper: resultWrapper } = await mountApp(AccessKeyResultPanel, {
      api,
      queryClient: new QueryClient(),
      mounting: { props: { result, secret: null, revealPending: false } },
    })
    await resultWrapper.get('[data-test="access-key-result-reveal"]').trigger('click')
    expect(resultWrapper.emitted('reveal')).toHaveLength(1)

    const { wrapper: feedbackWrapper } = await mountApp(AccessKeyOperationFeedback, {
      api,
      queryClient: new QueryClient(),
      mounting: {
        props: {
          failed: true,
          editNotApplied: false,
          revealFailed: true,
          mutationFeedbackKey: 'accessKeys.drawer.saveIndeterminate',
          scopeFeedbackKey: 'accessKeys.drawer.scopeIncomplete',
          showScopeFeedback: true,
        },
      },
    })
    expect(feedbackWrapper.text()).toContain('无法保存 AccessKey')
    expect(feedbackWrapper.text()).toContain('无法显示此 AccessKey')
  })
})
