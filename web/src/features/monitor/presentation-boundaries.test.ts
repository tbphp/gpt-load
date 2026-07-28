import { QueryClient } from '@tanstack/vue-query'

import { mountApp } from '@/test/mount-app'

import HealthProblemCollection from './HealthProblemCollection.vue'
import InspectorForm from './InspectorForm.vue'
import LogsFilterForm from './LogsFilterForm.vue'
import UsageFilterForm from './UsageFilterForm.vue'
import UsageSummary from './UsageSummary.vue'

const api = { request: vi.fn() }

describe('Monitor presentation boundaries', () => {
  it('emits one Usage Apply intent without owning navigation', async () => {
    const { wrapper } = await mountApp(UsageFilterForm, {
      api,
      queryClient: new QueryClient(),
      mounting: {
        props: {
          draft: { range: '24h', group_id: '', model: '' },
          errors: {},
          groups: [],
          groupsFailed: false,
          fetching: false,
        },
      },
    })
    await wrapper.get('[data-test="usage-range"]').setValue('30d')
    await wrapper.get('[data-test="usage-filter-form"]').trigger('submit')
    expect(wrapper.emitted('updateField')?.[0]).toEqual(['range', '30d'])
    expect(wrapper.emitted('apply')).toHaveLength(1)
  })

  it('emits Logs filter and refresh intents', async () => {
    const { wrapper } = await mountApp(LogsFilterForm, {
      api,
      queryClient: new QueryClient(),
      mounting: {
        props: {
          draft: {
            from: '',
            to: '',
            group_id: '',
            model: '',
            access_key_id: '',
            status: '',
            request_id: '',
          },
          errors: {},
          groups: [],
          accessKeys: [],
          groupsFailed: false,
          accessKeysFailed: false,
          dirty: false,
          refreshPending: false,
        },
      },
    })
    await wrapper.get('[data-test="logs-model"]').setValue('gpt-4.1')
    await wrapper.get('[data-test="logs-refresh"]').trigger('click')
    expect(wrapper.emitted('updateField')?.[0]).toEqual(['model', 'gpt-4.1'])
    expect(wrapper.emitted('refresh')).toHaveLength(1)
  })

  it('emits Inspector form intents and Health expansion intents', async () => {
    const { wrapper: form } = await mountApp(InspectorForm, {
      api,
      queryClient: new QueryClient(),
      mounting: {
        props: {
          protocol: '',
          model: '',
          accessKeyId: '',
          protocolOptions: [{ value: 'openai', label: 'OpenAI' }],
          accessKeyOptions: [],
          errors: {},
          optionsPending: false,
          optionsFailed: false,
          missingAccessKey: false,
        },
      },
    })
    await form.get('[data-test="inspector-model"]').setValue('gpt-4.1')
    expect(form.emitted('update:model')?.[0]).toEqual(['gpt-4.1'])

    const { wrapper: problems } = await mountApp(HealthProblemCollection, {
      api,
      queryClient: new QueryClient(),
      mounting: {
        props: {
          sections: [
            {
              kind: 'cooldown',
              label: 'Cooldown',
              tone: 'warning',
              keys: [
                {
                  key_id: 9,
                  group_id: 7,
                  group_name: 'Primary',
                  cooldown_until: null,
                  failure_count: 1,
                  recent_success_count: 0,
                  recent_failure_count: 1,
                  consecutive_failure_count: 1,
                  weight_manual: null,
                  weight_auto: 1,
                  recovery: { automatic: true, mode: 'cooldown_expiry', at: null },
                },
              ],
            },
          ],
          expandedKeyIds: new Set<number>(),
          remainingByKey: {},
        },
      },
    })
    await problems.get('[data-test="problem-key-9"]').trigger('click')
    expect(problems.emitted('toggle')?.[0]).toEqual([9])
  })

  it('renders Usage summary as a pure projection', async () => {
    const { wrapper } = await mountApp(UsageSummary, {
      api,
      queryClient: new QueryClient(),
      mounting: {
        props: {
          observedAt: '2026-07-29T00:00:00Z',
          summary: {
            request_count: 1,
            success_count: 1,
            failure_count: 0,
            uncached_input_tokens: 2,
            cache_read_tokens: 3,
            cache_write_5m_tokens: 4,
            cache_write_1h_tokens: 5,
            output_tokens: 6,
            total_tokens: 20,
            estimated_cost_usd: 0.1,
            usage_missing_count: 0,
            partial_count: 0,
            unpriced_request_count: 0,
          },
        },
      },
    })
    expect(wrapper.get('[data-test="usage-kpi-total-tokens"]').text()).toContain('20')
  })
})
