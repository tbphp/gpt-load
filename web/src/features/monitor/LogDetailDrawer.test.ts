import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import type { ApiClient } from '@/api/client'
import type {
  FailureCategory,
  RequestLogAction,
  RequestLogAttemptDto,
  RequestLogItemDto,
} from '@/api/control/request-logs'
import { mountApp } from '@/test/mount-app'

import LogDetailDrawer from './LogDetailDrawer.vue'

const requestID = 'a4d4e121-8ac3-4df4-8ceb-63b10ddc6173'

function attempt(
  sequence: number,
  overrides: Partial<RequestLogAttemptDto> = {},
): RequestLogAttemptDto {
  return {
    sequence,
    group_id: 7,
    group_name: 'Historical Group',
    key_id: 21,
    key_mask: 'sk-up…safe',
    upstream_model: 'gpt-upstream',
    status_code: 429,
    duration_ms: 40,
    failure_category: 'rate_limited',
    action: 'retry',
    will_retry: true,
    error_code: 'rate_limit',
    error_summary: '<b data-canary="summary-html">bounded upstream summary</b>',
    committed: false,
    ...overrides,
  }
}

function logFixture(overrides: Partial<RequestLogItemDto> = {}): RequestLogItemDto {
  return {
    request_id: requestID,
    completed_at: '2026-07-25T10:00:01Z',
    access_key: { id: 12, name: 'client', deleted: false },
    protocol: 'openai',
    client_model: 'gpt-client',
    upstream_model: 'gpt-upstream',
    status: 'error',
    status_code: 502,
    duration_ms: 125,
    error_code: 'upstream_error',
    error_summary: '<script data-canary="log-summary">unsafe()</script>',
    affinity_hit: false,
    attempts: [
      attempt(3, { group_name: 'Third Group', key_mask: 'sk-up…third' }),
      attempt(1),
      attempt(2, { group_name: 'Second Group', key_mask: 'sk-up…second' }),
    ],
    group_id: 7,
    usage_state: 'complete',
    cost_state: 'priced',
    uncached_input_tokens: 100,
    cache_read_tokens: 20,
    cache_write_5m_tokens: 3,
    cache_write_1h_tokens: 4,
    output_tokens: 50,
    estimated_cost_usd: 0.123,
    ...overrides,
  }
}

async function mountDrawer(log = logFixture()) {
  const mounted = await mountApp(LogDetailDrawer, {
    api: { request: vi.fn() as ApiClient['request'] },
    queryClient: new QueryClient({ defaultOptions: { queries: { retry: false } } }),
    path: '/monitor?tab=logs',
    locale: 'en-US',
    mounting: {
      props: { open: true, log },
      attachTo: document.body,
    },
  })
  await flushPromises()
  return mounted
}

function bodyElement<T extends Element>(selector: string): T {
  const element = document.body.querySelector<T>(selector)
  if (!element) throw new Error(`Missing ${selector}`)
  return element
}

describe('LogDetailDrawer', () => {
  it('renders ordered safe details with Request ID as the only copy target and a minimal Inspector link', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })

    await mountDrawer()

    expect(
      [...document.body.querySelectorAll<HTMLElement>('[data-test^="log-attempt-"]')].map(
        (element) => element.dataset.test,
      ),
    ).toEqual(['log-attempt-1', 'log-attempt-2', 'log-attempt-3'])
    expect(bodyElement('[data-test="log-attempt-1"]').textContent).toContain('Historical Group')
    expect(bodyElement('[data-test="log-attempt-1"]').textContent).toContain('sk-up…safe')
    expect(document.body.textContent).toContain(
      '<script data-canary="log-summary">unsafe()</script>',
    )
    expect(document.body.textContent).toContain(
      '<b data-canary="summary-html">bounded upstream summary</b>',
    )
    expect(document.body.querySelector('script[data-canary="log-summary"]')).toBeNull()
    expect(document.body.querySelector('b[data-canary="summary-html"]')).toBeNull()
    expect(document.body.textContent).not.toContain('Affinity hit')

    const copyButtons = [
      ...document.body.querySelectorAll<HTMLButtonElement>('button[aria-label^="Copy"]'),
    ]
    expect(copyButtons).toHaveLength(1)
    expect(copyButtons[0]?.getAttribute('aria-label')).toBe('Copy request ID')
    copyButtons[0]?.click()
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith(requestID)
    expect(document.body.textContent).not.toContain('Copy all')

    const inspectorLink = bodyElement<HTMLAnchorElement>('[data-test="log-inspector-link"]')
    expect(inspectorLink.getAttribute('href')).toBe(
      '/monitor?tab=inspector&protocol=openai&external_model=gpt-client&access_key_id=12',
    )
    expect(inspectorLink.textContent?.trim()).toBe(
      'Inspect with current state (not a historical replay)',
    )
    expect(document.body.querySelector('time')?.getAttribute('datetime')).toBe(
      '2026-07-25T10:00:01Z',
    )
  })

  it.each([
    ['protocol', { protocol: '' as RequestLogItemDto['protocol'] }],
    ['client model', { client_model: '' }],
    ['access key ID', { access_key: { id: 0, name: 'client', deleted: false } }],
  ] satisfies ReadonlyArray<[string, Partial<RequestLogItemDto>]>)(
    'does not render the Inspector deep link when the %s is unavailable',
    async (_field, overrides) => {
      await mountDrawer(logFixture(overrides))

      expect(document.body.querySelector('[data-test="log-inspector-link"]')).toBeNull()
    },
  )

  it('maps all failure categories and actions with one safe unknown fallback', async () => {
    const categories: FailureCategory[] = [
      'ok',
      'rate_limited',
      'model_unavailable',
      'invalid_key',
      'upstream_host_error',
      'client_error',
      'downstream_cancel',
      'ambiguous',
    ]
    const actions: RequestLogAction[] = [
      'terminate',
      'retry',
      'cooldown_key',
      'fail_key',
      'skip_group',
    ]
    const attempts = categories.map((failureCategory, index) =>
      attempt(index + 1, {
        failure_category: failureCategory,
        action: actions[index % actions.length],
      }),
    )
    attempts.push(
      attempt(9, {
        failure_category: 'future-secret-category' as FailureCategory,
        action: 'future-secret-action' as RequestLogAction,
      }),
    )

    await mountDrawer(logFixture({ attempts }))

    const rendered = document.body.textContent ?? ''
    for (const label of [
      'OK',
      'Rate limited',
      'Model unavailable',
      'Invalid key',
      'Upstream host error',
      'Client error',
      'Downstream canceled',
      'Ambiguous',
      'Terminate',
      'Retry',
      'Cooldown key',
      'Fail key',
      'Skip Group',
      'Unknown category',
      'Unknown action',
    ]) {
      expect(rendered).toContain(label)
    }
    expect(rendered).not.toContain('future-secret')
  })

  it.each([
    [
      'complete priced',
      { usage_state: 'complete', cost_state: 'priced', estimated_cost_usd: 0.123 },
      [
        'Complete usage',
        'Estimated cost available',
        'Included in default token and estimated-cost aggregates',
      ],
      false,
    ],
    [
      'complete unpriced',
      { usage_state: 'complete', cost_state: 'unpriced', estimated_cost_usd: 0 },
      ['Complete usage', 'Estimated cost unknown', 'Tokens included; estimated cost excluded'],
      true,
    ],
    [
      'partial priced',
      { usage_state: 'partial', cost_state: 'priced', estimated_cost_usd: 0.123 },
      [
        'Partial usage',
        'Estimated cost from reported tokens',
        'Reported tokens and estimated cost included',
      ],
      false,
    ],
    [
      'partial unpriced',
      { usage_state: 'partial', cost_state: 'unpriced', estimated_cost_usd: 0 },
      [
        'Partial usage',
        'Estimated cost unknown',
        'Reported tokens included; estimated cost excluded',
      ],
      true,
    ],
    [
      'missing unpriced',
      { usage_state: 'missing', cost_state: 'unpriced', estimated_cost_usd: 0 },
      [
        'Usage missing',
        'Estimated cost unknown',
        'Excluded from token and estimated-cost aggregates',
      ],
      true,
    ],
    [
      'not applicable',
      { usage_state: 'not_applicable', cost_state: 'not_applicable', estimated_cost_usd: 0 },
      ['Usage not applicable', 'Cost not applicable', 'Excluded from usage and cost aggregates'],
      false,
    ],
  ] satisfies ReadonlyArray<
    [string, Partial<RequestLogItemDto>, readonly [string, string, string], boolean]
  >)(
    'explains the valid %s usage/cost combination and its default aggregation behavior',
    async (_case, overrides, expected, hasPriceLink) => {
      await mountDrawer(logFixture(overrides))

      const section = bodyElement('[data-test="log-usage-cost"]')
      for (const text of expected) expect(section.textContent).toContain(text)
      expect(section.textContent).toContain('Estimated')
      expect(document.body.querySelector('[data-test="log-usage-prices-link"]') !== null).toBe(
        hasPriceLink,
      )
    },
  )

  it('renders final Group and every token category without collapsing cache-write windows', async () => {
    await mountDrawer()

    const section = bodyElement('[data-test="log-usage-cost"]')
    for (const fact of [
      'Final Group',
      '#7',
      'Uncached input',
      '100',
      'Cache read',
      '20',
      'Cache write (5m)',
      '3',
      'Cache write (1h)',
      '4',
      'Output',
      '50',
    ]) {
      expect(section.textContent).toContain(fact)
    }
  })

  it('renders an absent final Group as unknown rather than fabricating a Group', async () => {
    await mountDrawer(logFixture({ group_id: null }))

    expect(bodyElement('[data-test="log-final-group"]').textContent).toContain('Unknown')
  })

  it('does not infer unknown cost from numeric zero', async () => {
    await mountDrawer(logFixture({ cost_state: 'priced', estimated_cost_usd: 0 }))

    const cost = bodyElement('[data-test="log-estimated-cost"]').textContent ?? ''
    expect(cost).toContain('$0.000000')
    expect(cost).not.toContain('Unknown')
    expect(cost).not.toContain('Free')
  })
})
