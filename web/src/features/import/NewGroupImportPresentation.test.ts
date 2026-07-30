import { QueryClient } from '@tanstack/vue-query'

import { mountApp } from '@/test/mount-app'

import ImportConnectionStep from './ImportConnectionStep.vue'
import ImportModelsStep from './ImportModelsStep.vue'
import ImportOperationNotice from './ImportOperationNotice.vue'
import ImportReviewStep from './ImportReviewStep.vue'

const api = { request: vi.fn() }

describe('new Group import presentation boundaries', () => {
  it('emits connection and discovery intents', async () => {
    const { wrapper } = await mountApp(ImportConnectionStep, {
      api,
      queryClient: new QueryClient(),
      mounting: {
        props: {
          presetId: 'openai',
          name: '',
          upstreamUrl: 'https://api.openai.com',
          protocols: ['openai-chat-completions', 'openai-responses'],
          keys: '',
          headerRules: { set: {}, remove: [] },
          pending: false,
          canDiscover: true,
        },
      },
    })

    await wrapper.get('[data-test="group-name"]').setValue('Primary')
    await wrapper.get('[data-test="discover"]').trigger('click')

    expect(
      wrapper.findAll('[data-test^="protocol-"]').map((option) => option.attributes('data-test')),
    ).toEqual([
      'protocol-openai-chat-completions',
      'protocol-openai-responses',
      'protocol-anthropic',
      'protocol-gemini',
    ])
    expect(wrapper.get('[data-test="import-responses-affinity-warning"]').text()).toContain(
      '上游 Key',
    )
    expect(wrapper.emitted('update:name')?.[0]).toEqual(['Primary'])
    expect(wrapper.emitted('discover')).toHaveLength(1)
  })

  it('emits model and review intents', async () => {
    const { wrapper } = await mountApp(ImportModelsStep, {
      api,
      queryClient: new QueryClient(),
      mounting: {
        props: {
          discoveryFailed: true,
          manualMode: false,
          errorKey: 'import.discoveryFailed',
          models: [],
          canReview: false,
          resourceOnly: false,
        },
      },
    })

    await wrapper.get('[data-test="manual-path"]').trigger('click')
    expect(wrapper.emitted('manual')).toHaveLength(1)
  })

  it('keeps operation and review actions as parent-owned intents', async () => {
    const { wrapper: notice } = await mountApp(ImportOperationNotice, {
      api,
      queryClient: new QueryClient(),
      mounting: {
        props: {
          messageKey: 'import.operation.indeterminate',
          resourceIdentity: '',
          canRetry: true,
          pending: false,
        },
      },
    })
    await notice.get('[data-test="import-operation-retry"]').trigger('click')
    expect(notice.emitted('retry')).toHaveLength(1)

    const { wrapper: review } = await mountApp(ImportReviewStep, {
      api,
      queryClient: new QueryClient(),
      mounting: {
        props: {
          name: '',
          upstreamUrl: 'https://api.openai.com',
          protocols: ['openai-chat-completions', 'openai-responses'],
          keyCount: 1,
          models: [{ id: 'gpt-4.1', aliases: [] }],
          resourceOnly: false,
          errorKey: '',
          conflict: null,
          pending: false,
          operationNoticeActive: false,
        },
      },
    })
    await review.get('[data-test="create"]').trigger('click')
    expect(review.emitted('create')).toHaveLength(1)
  })
})
