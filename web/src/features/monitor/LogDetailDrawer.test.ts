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

    const copyButtons = [
      ...document.body.querySelectorAll<HTMLButtonElement>('button[aria-label^="Copy"]'),
    ]
    expect(copyButtons).toHaveLength(1)
    expect(copyButtons[0]?.getAttribute('aria-label')).toBe('Copy request ID')
    copyButtons[0]?.click()
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith(requestID)
    expect(document.body.textContent).not.toContain('Copy all')

    expect(
      bodyElement<HTMLAnchorElement>('[data-test="log-inspector-link"]').getAttribute('href'),
    ).toBe('/monitor?tab=inspector&protocol=openai&external_model=gpt-client&access_key_id=12')
    expect(document.body.querySelector('time')?.getAttribute('datetime')).toBe(
      '2026-07-25T10:00:01Z',
    )
  })

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
})
