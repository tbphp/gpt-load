import { QueryClient } from '@tanstack/vue-query'

import type { UsageReportDto } from '@/app/resources/usage'
import { mountApp } from '@/test/mount-app'

import HomeCostRanking from './HomeCostRanking.vue'
import homeCostRankingSource from './HomeCostRanking.vue?raw'

const aggregate = {
  request_count: 10,
  success_count: 9,
  failure_count: 1,
  uncached_input_tokens: 100,
  cache_read_tokens: 20,
  cache_write_5m_tokens: 0,
  cache_write_1h_tokens: 0,
  output_tokens: 40,
  total_tokens: 160,
  estimated_cost_usd: 3,
  usage_missing_count: 0,
  partial_count: 0,
  unpriced_request_count: 0,
}

const report: UsageReportDto = {
  range: '24h',
  granularity: 'hour',
  timezone: 'UTC',
  from: '2026-07-28T07:00:00Z',
  to: '2026-07-29T07:00:00Z',
  observed_at: '2026-07-29T06:00:00Z',
  summary: aggregate,
  series: [],
  breakdown: Array.from({ length: 6 }, (_, index) => ({
    ...aggregate,
    group_id: index + 1,
    model: index === 0 ? 'model / needs encoding' : `model-${index + 1}`,
    estimated_cost_usd: 6 - index,
  })),
  breakdown_truncated: false,
  breakdown_order: 'cost',
  breakdown_group_count: 14,
  collection_health: {
    scope: 'current_process',
    dropped_total: 0,
    write_failure_total: 0,
    last_write_failure_at: null,
  },
}

describe('HomeCostRanking', () => {
  it('renders the backend-order Top 5, responsive token column and distinct Group footer', async () => {
    const { wrapper } = await mountApp(HomeCostRanking, {
      api: { request: vi.fn() },
      queryClient: new QueryClient(),
      mounting: {
        props: {
          report,
          groupNames: new Map([[1, 'Primary']]),
        },
      },
    })
    const rows = wrapper.findAll('[data-ranking-row]')

    expect(rows).toHaveLength(5)
    expect(rows.map((row) => row.get('[data-ranking-model]').text())).toEqual([
      'model / needs encoding',
      'model-2',
      'model-3',
      'model-4',
      'model-5',
    ])
    expect(wrapper.get('th[data-column-priority="low"]').text()).toContain('Token')
    expect(wrapper.findAll('td[data-column-priority="low"]')).toHaveLength(5)
    expect(wrapper.get('[data-test="home-ranking-footer"]').text()).toContain('共 14 个 Group')
  })

  it('delegates URL encoding to RouterLink and never resorts the projected rows', () => {
    expect(homeCostRankingSource).not.toMatch(/encodeURIComponent|\.sort\(/)
    expect(homeCostRankingSource).toContain('usageBreakdownLocation')
    expect(homeCostRankingSource).toContain('DataTable')
  })
})
