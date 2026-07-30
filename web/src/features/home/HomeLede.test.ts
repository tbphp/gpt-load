import { QueryClient } from '@tanstack/vue-query'

import type { RuntimeHealthDto } from '@/app/resources/health'
import { mountApp } from '@/test/mount-app'

import HomeLede from './HomeLede.vue'
import homeLedeSource from './HomeLede.vue?raw'
import type { HomeHealthState } from './home-presenter'

const api = { request: vi.fn() }

function health(overrides: Partial<RuntimeHealthDto> = {}): RuntimeHealthDto {
  return {
    observed_at: '2026-07-29T06:32:07Z',
    snapshot_revision: 28,
    stats_window_seconds: 300,
    counts: { total: 42, available: 38, cooldown: 3, blacklisted: 1, disabled: 0 },
    groups: Array.from({ length: 14 }, (_, index) => ({
      id: index + 1,
      name: `Group ${index + 1}`,
      enabled: true,
      counts:
        index < 12
          ? { total: 3, available: 3, cooldown: 0, blacklisted: 0, disabled: 0 }
          : { total: 3, available: 1, cooldown: 1, blacklisted: 0, disabled: 1 },
    })),
    cooldown_keys: [],
    blacklisted_keys: [],
    request_log: {
      enqueued_total: 0,
      persisted_total: 0,
      dropped_not_running_total: 0,
      dropped_queue_full_total: 0,
      dropped_stopping_total: 0,
      dropped_persist_failed_total: 0,
      dropped_shutdown_total: 0,
      dropped_total: 0,
      write_failure_total: 0,
      retention_delete_failure_total: 0,
      queue_depth: 0,
      queue_capacity: 0,
      last_write_failure_at: null,
      last_retention_failure_at: null,
    },
    ...overrides,
  }
}

async function mountLede(state: HomeHealthState) {
  return (
    await mountApp(HomeLede, {
      api,
      queryClient: new QueryClient(),
      mounting: {
        props: {
          state,
          observedDate: '2026-07-29',
        },
      },
    })
  ).wrapper
}

describe('HomeLede', () => {
  it('renders one normal H1 with the Ledger conclusion and observation stamp', async () => {
    const normalHealth = health({
      counts: { total: 42, available: 38, cooldown: 0, blacklisted: 0, disabled: 4 },
      groups: health().groups.map((group) => ({
        ...group,
        counts: { total: 3, available: 3, cooldown: 0, blacklisted: 0, disabled: 0 },
      })),
    })
    const wrapper = await mountLede({ kind: 'normal', health: normalHealth })

    expect(wrapper.findAll('h1')).toHaveLength(1)
    expect(wrapper.get('h1').text()).toBe('14 个分组运行正常，38/42 把密钥可用。')
    expect(wrapper.get('h1').text()).not.toContain('Group')
    expect(wrapper.classes()).toContain('home-lede--normal')
    expect(wrapper.text()).toContain('2026-07-29')
    expect(wrapper.get('[data-test="home-observed-time"]').text()).toMatch(/14:32:07|06:32:07/)
    expect(wrapper.get('[data-test="home-observed-time"]').text()).not.toMatch(/2026|GMT/)
    expect(wrapper.text()).toContain('配置版本 rev.28')
  })

  it('uses warning emphasis for problem health without turning the lede danger', async () => {
    const report = health()
    const wrapper = await mountLede({
      kind: 'problem',
      health: report,
      groups: [
        {
          groupId: 13,
          groupName: 'Group 13',
          cooldownKeys: [],
          blacklistedKeys: [],
        },
        {
          groupId: 14,
          groupName: 'Group 14',
          cooldownKeys: [],
          blacklistedKeys: [],
        },
      ],
    })

    expect(wrapper.get('h1').text()).toBe(
      '12 个分组运行正常，2 个分组存在密钥异常：38/42 把密钥可用。',
    )
    expect(wrapper.get('[data-test="home-lede-problem-emphasis"]').text()).toBe(
      '2 个分组存在密钥异常：',
    )
    expect(wrapper.get('[data-test="home-lede-problem-emphasis"]').classes()).toContain(
      'home-lede__problem-emphasis',
    )
    expect(wrapper.classes()).toContain('home-lede--warning')
    expect(wrapper.classes()).not.toContain('home-lede--danger')
  })

  it('renders unknown health as neutral with one Retry intent', async () => {
    const wrapper = await mountLede({ kind: 'unknown', retryable: true })

    expect(wrapper.get('h1').text()).toContain('无法确认服务状态')
    expect(wrapper.classes()).toContain('home-lede--neutral')
    expect(homeLedeSource).toMatch(
      /\.home-lede--neutral h1\s*\{[\s\S]*color: var\(--color-text-muted\);/,
    )
    await wrapper.get('[data-test="home-health-retry"]').trigger('click')
    expect(wrapper.emitted('retry')).toHaveLength(1)
  })

  it('keeps a cached conclusion visibly stale instead of presenting it as current', async () => {
    const wrapper = await mountLede({
      kind: 'stale',
      health: health({
        cooldown_keys: [],
        blacklisted_keys: [],
      }),
      failedAt: '2026-07-29T06:34:00Z',
    })

    expect(wrapper.get('h1').text()).toContain('最近一次观测')
    expect(wrapper.text()).toContain('当前健康检查失败')
    expect(wrapper.classes()).toContain('home-lede--neutral')
  })

  it('caps the editorial conclusion instead of scaling it to dashboard-display size', () => {
    expect(homeLedeSource).toMatch(
      /\.home-lede h1\s*\{[\s\S]*font-size: clamp\(2rem, 2\.5vw, 2\.25rem\);/,
    )
    expect(homeLedeSource).toMatch(
      /\.home-lede\s*\{[\s\S]*padding: var\(--space-2\) 0 var\(--space-7\);/,
    )
    expect(homeLedeSource).toMatch(/\.home-lede h1\s*\{[\s\S]*max-width: 34ch;/)
    expect(homeLedeSource).toMatch(
      /\.home-lede--normal \.home-lede__eyebrow svg\s*\{[\s\S]*background: var\(--color-success-bg\);/,
    )
    expect(homeLedeSource).toMatch(
      /\.home-lede--warning \.home-lede__eyebrow svg\s*\{[\s\S]*background: var\(--color-warning-bg\);/,
    )
    expect(homeLedeSource).not.toContain('3.35rem')
  })
})
