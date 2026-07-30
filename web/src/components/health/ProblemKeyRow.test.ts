import { mount } from '@vue/test-utils'

import type { HealthProblemKeyDto } from '@/app/resources/health'

import ProblemKeyRow from './ProblemKeyRow.vue'
import problemKeyRowSource from './ProblemKeyRow.vue?raw'

const labels = {
  consecutiveFailures: 'Consecutive failures',
  failureCategory: 'Failure category',
  statusCode: 'Status code',
  statusUnavailable: 'No HTTP status',
  recoversAt: 'Recovers at',
  validationProbe: 'Validation probe',
}

function problemKey(overrides: Partial<HealthProblemKeyDto> = {}): HealthProblemKeyDto {
  return {
    key_id: 11,
    group_id: 7,
    group_name: 'Primary',
    cooldown_until: '2026-07-29T06:36:00Z',
    failure_count: 8,
    recent_success_count: 1,
    recent_failure_count: 5,
    consecutive_failure_count: 5,
    weight_manual: null,
    weight_auto: 40,
    recovery: {
      automatic: true,
      mode: 'cooldown_expiry',
      at: '2026-07-29T06:36:00Z',
    },
    mask: '8f2a****91mk',
    last_failure_category: 'rate_limited',
    last_status_code: 429,
    ...overrides,
  }
}

function mountRow(
  key: HealthProblemKeyDto,
  options: {
    tone?: 'warning' | 'danger'
    statusLabel?: string
    failureCategoryLabel?: string
  } = {},
) {
  return mount(ProblemKeyRow, {
    props: {
      problemKey: key,
      tone: options.tone ?? 'warning',
      statusLabel: options.statusLabel ?? 'Cooldown',
      failureCategoryLabel: options.failureCategoryLabel ?? 'Rate limited',
      labels,
      locale: 'en-US',
    },
  })
}

describe('ProblemKeyRow', () => {
  it('renders a cooldown mask, 429 context and locale-formatted recovery time', () => {
    const key = problemKey()
    const wrapper = mountRow(key)
    const expectedRecovery = new Intl.DateTimeFormat('en-US', {
      dateStyle: 'medium',
      timeStyle: 'short',
    }).format(new Date(key.recovery.at!))

    expect(wrapper.get('[data-problem-key-mask]').text()).toBe('8f2a****91mk')
    expect(wrapper.get('[data-problem-key-status]').classes()).toContain('status-badge--warning')
    expect(wrapper.get('[data-problem-key-status] svg').attributes('aria-hidden')).toBe('true')
    expect(wrapper.get('[data-problem-key-failures]').text()).toBe('5')
    expect(wrapper.get('[data-problem-key-category]').text()).toBe('Rate limited')
    expect(wrapper.get('[data-problem-key-http-status]').text()).toBe('429')
    expect(wrapper.get('time').attributes('datetime')).toBe(key.recovery.at)
    expect(wrapper.get('time').text()).toBe(expectedRecovery)
  })

  it('renders a blacklisted 401 state with danger tone', () => {
    const wrapper = mountRow(
      problemKey({
        cooldown_until: undefined,
        mask: 'c73d****k0pl',
        last_failure_category: 'invalid_key',
        last_status_code: 401,
        recovery: { automatic: true, mode: 'validation_probe', at: null },
      }),
      {
        tone: 'danger',
        statusLabel: 'Blacklisted',
        failureCategoryLabel: 'Invalid key',
      },
    )

    expect(wrapper.get('[data-problem-key-status]').classes()).toContain('status-badge--danger')
    expect(wrapper.get('[data-problem-key-category]').text()).toBe('Invalid key')
    expect(wrapper.get('[data-problem-key-http-status]').text()).toBe('401')
    expect(wrapper.get('[data-problem-key-recovery]').text()).toBe('Validation probe')
  })

  it('keeps an ambiguous failure without an HTTP response explicit', () => {
    const wrapper = mountRow(
      problemKey({
        cooldown_until: undefined,
        mask: '****',
        last_failure_category: 'ambiguous',
        last_status_code: null,
        recovery: { automatic: true, mode: 'validation_probe', at: null },
      }),
      { failureCategoryLabel: 'Ambiguous failure' },
    )

    expect(wrapper.get('[data-problem-key-category]').text()).toBe('Ambiguous failure')
    expect(wrapper.get('[data-problem-key-http-status]').text()).toBe('No HTTP status')
    expect(wrapper.find('time').exists()).toBe(false)
  })

  it('is a pure projected row without query or secret-bearing props', () => {
    expect(problemKeyRowSource).not.toMatch(/useQuery|ApiClient|ciphertext|plaintext|api[_-]?key/i)
    expect(problemKeyRowSource).toContain('HealthProblemKeyDto')
  })
})
