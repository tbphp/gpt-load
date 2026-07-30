import type { Page, Route } from '@playwright/test'

import { visualClock, visualFixtureData, type VisualScenario } from './visual-fixtures'

export const visualAuthKey = 'visual-fixture-auth'

const group = {
  id: 7,
  name: visualFixtureData.groupName,
  upstream_url: 'https://visual-fixture.example/v1',
  protocols: ['openai-chat-completions', 'openai-responses'],
  models: [
    { id: visualFixtureData.modelName, alias: 'stable-public-alias' },
    { id: 'visual-fixture-secondary-model', alias: '' },
  ],
  enabled: true,
  key_count: 4,
}

const accessKey = {
  id: 12,
  name: visualFixtureData.longIdentifier,
  masked_key: `sk-gl-••••••••${visualFixtureData.accessKeySuffix}`,
  status: 'active',
  filters: {
    groups: [group.id],
    protocols: ['openai-chat-completions', 'openai-responses'],
    models: [visualFixtureData.modelName],
  },
  rpm_limit: 120,
  created_at: '2026-07-28T10:00:00.000Z',
  updated_at: visualClock,
}

const requestLogHealth = {
  enqueued_total: 8,
  persisted_total: 6,
  dropped_not_running_total: 0,
  dropped_queue_full_total: 1,
  dropped_stopping_total: 0,
  dropped_persist_failed_total: 1,
  dropped_shutdown_total: 0,
  dropped_total: 2,
  write_failure_total: 1,
  retention_delete_failure_total: 0,
  queue_depth: 2,
  queue_capacity: 256,
  last_write_failure_at: '2026-07-28T23:50:00.000Z',
  last_retention_failure_at: null,
}

const normalHealth = {
  observed_at: visualClock,
  snapshot_revision: 42,
  stats_window_seconds: 300,
  counts: { total: 4, available: 4, cooldown: 0, blacklisted: 0, disabled: 0 },
  groups: [
    {
      id: group.id,
      name: group.name,
      enabled: true,
      counts: { total: 4, available: 4, cooldown: 0, blacklisted: 0, disabled: 0 },
    },
  ],
  cooldown_keys: [],
  blacklisted_keys: [],
  request_log: {
    ...requestLogHealth,
    dropped_queue_full_total: 0,
    dropped_persist_failed_total: 0,
    dropped_total: 0,
    write_failure_total: 0,
    last_write_failure_at: null,
  },
}

const anomalyHealth = {
  ...normalHealth,
  counts: { total: 4, available: 1, cooldown: 1, blacklisted: 1, disabled: 1 },
  groups: [
    {
      id: group.id,
      name: group.name,
      enabled: true,
      counts: { total: 4, available: 1, cooldown: 1, blacklisted: 1, disabled: 1 },
    },
  ],
  cooldown_keys: [
    {
      key_id: 91,
      group_id: group.id,
      group_name: group.name,
      cooldown_until: '2026-07-29T00:10:00.000Z',
      failure_count: 3,
      recent_success_count: 1,
      recent_failure_count: 3,
      consecutive_failure_count: 2,
      weight_manual: null,
      weight_auto: 35,
      recovery: {
        automatic: true,
        mode: 'cooldown_expiry',
        at: '2026-07-29T00:10:00.000Z',
      },
    },
  ],
  blacklisted_keys: [
    {
      key_id: 92,
      group_id: group.id,
      group_name: group.name,
      failure_count: 8,
      recent_success_count: 0,
      recent_failure_count: 5,
      consecutive_failure_count: 5,
      weight_manual: 50,
      weight_auto: 0,
      recovery: { automatic: true, mode: 'validation_probe', at: null },
    },
  ],
  request_log: requestLogHealth,
}

const aggregate = {
  request_count: 6,
  success_count: 4,
  failure_count: 2,
  uncached_input_tokens: 1200,
  cache_read_tokens: 300,
  cache_write_5m_tokens: 120,
  cache_write_1h_tokens: 80,
  output_tokens: 500,
  total_tokens: 2200,
  estimated_cost_usd: 0.01234,
  usage_missing_count: 1,
  partial_count: 1,
  unpriced_request_count: 2,
}

const usageReport = {
  range: '24h',
  granularity: 'hour',
  timezone: 'UTC',
  from: '2026-07-28T00:00:00.000Z',
  to: '2026-07-29T00:00:00.000Z',
  observed_at: '2026-07-28T23:59:59.000Z',
  summary: aggregate,
  series: [
    {
      ...aggregate,
      bucket_start: '2026-07-28T23:00:00.000Z',
      bucket_end: '2026-07-29T00:00:00.000Z',
    },
  ],
  breakdown: [{ ...aggregate, group_id: group.id, model: visualFixtureData.modelName }],
  breakdown_truncated: true,
  collection_health: {
    scope: 'current_process',
    dropped_total: 2,
    write_failure_total: 1,
    last_write_failure_at: '2026-07-28T23:50:00.000Z',
  },
}

const settings = {
  values: {
    connect_timeout: 15,
    first_byte_timeout: 120,
    request_timeout: 600,
    stream_idle_timeout: 300,
    header_rules: { set: { 'X-Visual-Fixture': 'stable-value' }, remove: ['X-Legacy'] },
    inject_usage_options: true,
    request_log_retention_days: 7,
  },
  overrides: [],
}

const modelPrices = {
  price_unit: 'usd_per_million_tokens',
  builtin: [
    {
      pattern: 'visual-model-*',
      source: 'builtin',
      prices: {
        uncached_input: 1.25,
        cache_read: 0,
        cache_write_5m: null,
        cache_write_1h: 2.5,
        output: 7.5,
      },
      source_url: 'https://visual-fixture.example/pricing',
      updated_at: visualClock,
      pricing_policy: {
        input_threshold_tokens: 272000,
        input_multiplier: 2,
        output_multiplier: 1.5,
      },
    },
  ],
  overrides: [
    {
      pattern: '*',
      source: 'user',
      prices: {
        uncached_input: 2,
        cache_read: 0,
        cache_write_5m: null,
        cache_write_1h: null,
        output: 8,
      },
      source_url: null,
      updated_at: visualClock,
      pricing_policy: null,
    },
  ],
}

const requestLogPage = {
  items: [
    {
      request_id: visualFixtureData.requestId,
      completed_at: visualClock,
      access_key: { id: accessKey.id, name: accessKey.name, deleted: false },
      protocol: 'openai-chat-completions',
      client_model: 'stable-public-alias',
      upstream_model: visualFixtureData.modelName,
      status: 'error',
      status_code: 429,
      duration_ms: 842,
      error_code: 'RATE_LIMITED',
      error_summary: 'Fixture upstream rate limit',
      affinity_hit: false,
      attempts: [
        {
          sequence: 1,
          group_id: group.id,
          group_name: group.name,
          key_id: 91,
          upstream_model: visualFixtureData.modelName,
          status_code: 429,
          duration_ms: 842,
          failure_category: 'rate_limited',
          action: 'cooldown_key',
          will_retry: false,
          error_code: 'RATE_LIMITED',
          error_summary: 'Fixture upstream rate limit',
          committed: true,
        },
      ],
      group_id: group.id,
      usage_state: 'missing',
      cost_state: 'unpriced',
      uncached_input_tokens: 0,
      cache_read_tokens: 0,
      cache_write_5m_tokens: 0,
      cache_write_1h_tokens: 0,
      output_tokens: 0,
      estimated_cost_usd: 0,
    },
  ],
  next_cursor: null,
}

const routeInspection = {
  observed_at: visualClock,
  snapshot_revision: 42,
  protocol: 'openai-chat-completions',
  external_model: 'stable-public-alias',
  access_key: { id: accessKey.id, name: accessKey.name, status: 'active' },
  routable: true,
  reason_code: null,
  groups: [
    {
      group_id: group.id,
      group_name: group.name,
      upstream_model: visualFixtureData.modelName,
      weight_manual: null,
      included: true,
      routable: true,
      reason_code: null,
      keys: [
        {
          key_id: 91,
          available: true,
          reason_code: null,
          weight_manual: null,
          weight_auto: 72,
          effective_weight: 5184,
          cooldown_until: null,
        },
      ],
    },
  ],
}

function success(data: unknown, headers: Record<string, string> = {}) {
  return {
    status: 200,
    headers: { 'content-type': 'application/json; charset=utf-8', ...headers },
    body: JSON.stringify({ code: 0, message: 'OK', data }),
  }
}

function failure() {
  return {
    status: 503,
    headers: { 'content-type': 'application/json; charset=utf-8' },
    body: JSON.stringify({
      code: 'SERVICE_UNAVAILABLE',
      message: 'Fixture service unavailable',
    }),
  }
}

export interface VisualApiState {
  healthRequests: number
  unexpectedRequests: string[]
}

export async function installVisualApi(
  page: Page,
  scenario: VisualScenario,
): Promise<VisualApiState> {
  const state: VisualApiState = { healthRequests: 0, unexpectedRequests: [] }
  await page.route('**/api/**', async (route: Route) => {
    const request = route.request()
    const url = new URL(request.url())
    const method = request.method()

    if (url.pathname === '/api/auth/session') {
      await route.fulfill(success({ authenticated: true }))
      return
    }
    if (url.pathname === '/api/groups' && method === 'GET') {
      await route.fulfill(success(scenario === 'home-empty-error' ? [] : [group]))
      return
    }
    if (url.pathname === '/api/access-keys/options' && method === 'GET') {
      await route.fulfill(
        success(
          scenario === 'home-empty-error'
            ? []
            : [{ id: accessKey.id, name: accessKey.name, status: accessKey.status }],
        ),
      )
      return
    }
    if (url.pathname === '/api/access-keys' && method === 'GET') {
      await route.fulfill(success([accessKey]))
      return
    }
    if (
      url.pathname === '/api/access-keys' &&
      method === 'POST' &&
      scenario === 'access-key-operation'
    ) {
      await route.abort('connectionreset')
      return
    }
    if (url.pathname === '/api/health' && method === 'GET') {
      state.healthRequests += 1
      if (
        scenario === 'home-empty-error' ||
        (scenario === 'home-anomaly' && state.healthRequests > 1)
      ) {
        await route.fulfill(failure())
        return
      }
      await route.fulfill(success(scenario === 'home-anomaly' ? anomalyHealth : normalHealth))
      return
    }
    if (url.pathname === '/api/usage' && method === 'GET') {
      await route.fulfill(scenario === 'home-empty-error' ? failure() : success(usageReport))
      return
    }
    if (url.pathname === '/api/logs' && method === 'GET') {
      await route.fulfill(success(requestLogPage))
      return
    }
    if (url.pathname === '/api/settings' && method === 'GET') {
      await route.fulfill(
        success(settings, {
          etag: '"sha256-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"',
        }),
      )
      return
    }
    if (url.pathname === '/api/system/info' && method === 'GET') {
      await route.fulfill(
        success({
          version: '2.0.0-visual',
          deployment: {
            instance_mode: 'single',
            database: 'sqlite',
            distribution: 'single_binary',
          },
          data_dir: '/visual/data',
          auth_key: { source: 'environment', path: null },
          encryption: { enabled: true, source: 'environment', path: null },
        }),
      )
      return
    }
    if (url.pathname === '/api/model-prices' && method === 'GET') {
      await route.fulfill(success(modelPrices))
      return
    }
    if (url.pathname === '/api/route/inspect' && method === 'POST') {
      await route.fulfill(success(routeInspection))
      return
    }

    const identity = `${method} ${url.pathname}${url.search}`
    state.unexpectedRequests.push(identity)
    await route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({
        code: 'VISUAL_FIXTURE_UNEXPECTED_REQUEST',
        message: identity,
      }),
    })
  })
  return state
}
