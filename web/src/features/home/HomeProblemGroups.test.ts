import { QueryClient } from '@tanstack/vue-query'

import { mountApp } from '@/test/mount-app'

import HomeProblemGroups from './HomeProblemGroups.vue'
import homeProblemGroupsSource from './HomeProblemGroups.vue?raw'
import type { HomeProblemGroup } from './home-presenter'

const groups: HomeProblemGroup[] = [
  {
    groupId: 7,
    groupName: 'Primary',
    cooldownKeys: [
      {
        key_id: 11,
        group_id: 7,
        group_name: 'Primary',
        cooldown_until: '2026-07-29T06:36:00Z',
        failure_count: 5,
        recent_success_count: 0,
        recent_failure_count: 5,
        consecutive_failure_count: 5,
        weight_manual: null,
        weight_auto: 40,
        recovery: {
          automatic: true,
          mode: 'cooldown_expiry',
          at: '2026-07-29T06:36:00Z',
        },
        mask: 'rate****safe',
        last_failure_category: 'rate_limited',
        last_status_code: 429,
      },
    ],
    blacklistedKeys: [
      {
        key_id: 12,
        group_id: 7,
        group_name: 'Primary',
        failure_count: 12,
        recent_success_count: 0,
        recent_failure_count: 8,
        consecutive_failure_count: 12,
        weight_manual: null,
        weight_auto: 0,
        recovery: { automatic: true, mode: 'validation_probe', at: null },
        mask: 'inva****lock',
        last_failure_category: 'invalid_key',
        last_status_code: 401,
      },
    ],
  },
]

describe('HomeProblemGroups', () => {
  it('renders warning and danger problem rows plus the canonical problem-key deep link', async () => {
    const { wrapper } = await mountApp(HomeProblemGroups, {
      api: { request: vi.fn() },
      queryClient: new QueryClient(),
      mounting: { props: { groups } },
    })

    expect(wrapper.get('[data-problem-kind="cooldown"]').classes()).toContain(
      'home-problem-status--warning',
    )
    expect(wrapper.get('[data-problem-kind="blacklisted"]').classes()).toContain(
      'home-problem-status--danger',
    )
    expect(wrapper.text()).toContain('rate****safe')
    expect(wrapper.text()).toContain('inva****lock')
    expect(
      wrapper.get<HTMLAnchorElement>('[data-test="home-problem-link"]').attributes('href'),
    ).toBe('/groups/7?tab=keys&key_state=problem')
  })

  it('stays a pure projection with no resource query', () => {
    expect(homeProblemGroupsSource).not.toMatch(/useQuery|ApiClient|healthQueryOptions/)
    expect(homeProblemGroupsSource).toContain('ProblemKeyRow')
  })
})
