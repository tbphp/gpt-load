import type { SettingsDto } from '@/api/control/settings'
import type { SettingsResource } from '@/app/resources/settings'

import { createSettingsDraft } from './settings-patch'
import {
  chooseSettingsMutationResult,
  mergeSettingsConflict,
  reconcileSettingsMutation,
} from './settings-response'

function settings(connectTimeout: number, requestTimeout = 600): SettingsDto {
  return {
    values: {
      connect_timeout: connectTimeout,
      first_byte_timeout: 120,
      request_timeout: requestTimeout,
      stream_idle_timeout: 300,
      header_rules: { set: {}, remove: [] },
      inject_usage_options: false,
      request_log_retention_days: 7,
    },
    overrides: [],
  }
}

function resource(token: string, value: SettingsDto): SettingsResource {
  return { settings: value, settings_etag: `sha256-${token.repeat(64)}` }
}

describe('Settings response identity and three-way merge', () => {
  it('merges 412 conflicts across both Settings sections in one all-scope operation', () => {
    const base = resource('a', settings(15))
    const draft = createSettingsDraft(base.settings)
    draft.overrides.add('request_timeout')
    draft.values.request_timeout = 900
    draft.overrides.add('request_log_retention_days')
    draft.values.request_log_retention_days = 30
    const latestSettings = settings(15, 1_200)
    latestSettings.values.request_log_retention_days = 60
    latestSettings.overrides = ['request_timeout', 'request_log_retention_days']

    const result = mergeSettingsConflict(base, draft, resource('b', latestSettings), 'all')

    expect(result.conflicts.map(({ key }) => key)).toEqual([
      'request_timeout',
      'request_log_retention_days',
    ])
  })

  it('confirms an all-scope unknown write only after every intended field is observed', () => {
    const base = resource('a', settings(15))
    const draft = createSettingsDraft(base.settings)
    draft.overrides.add('request_timeout')
    draft.values.request_timeout = 900
    draft.overrides.add('request_log_retention_days')
    draft.values.request_log_retention_days = 30
    const partiallyApplied = settings(15)
    partiallyApplied.values.request_log_retention_days = 30
    partiallyApplied.overrides = ['request_log_retention_days']
    const fullyApplied = structuredClone(partiallyApplied)
    fullyApplied.values.request_timeout = 900
    fullyApplied.overrides.push('request_timeout')

    expect(
      reconcileSettingsMutation(base, draft, resource('b', partiallyApplied), 'all').kind,
    ).toBe('indeterminate')
    expect(reconcileSettingsMutation(base, draft, resource('c', fullyApplied), 'all').kind).toBe(
      'confirmed',
    )
  })

  it('accepts a response only when cache still matches the operation base or the response ETag', () => {
    const base = resource('a', settings(15))
    const response = resource('b', settings(30))
    const matchingResponseCache = resource('b', settings(30))
    const draft = createSettingsDraft(base.settings)
    draft.overrides.add('request_timeout')
    draft.values.request_timeout = 900

    for (const cached of [base, matchingResponseCache]) {
      const result = chooseSettingsMutationResult(
        response,
        cached,
        base,
        draft,
        'request-forwarding',
      )
      expect(result.kind).toBe('apply')
      if (result.kind === 'apply') {
        expect(result.resource).toBe(response)
        expect(result.draft.values.request_timeout).toBe(900)
        expect(result.draft.values.connect_timeout).toBe(30)
      }
    }
  })

  it('requires a refetch when cache is absent or has an unrelated ETag', () => {
    const base = resource('a', settings(15))
    const response = resource('b', settings(30))
    const unrelated = resource('c', settings(45))
    const draft = createSettingsDraft(base.settings)

    expect(
      chooseSettingsMutationResult(response, undefined, base, draft, 'request-forwarding'),
    ).toEqual({ kind: 'refetch' })
    expect(
      chooseSettingsMutationResult(response, unrelated, base, draft, 'request-forwarding'),
    ).toEqual({ kind: 'refetch' })
  })

  it('merges independent mine/latest changes and keeps the latest ETag', () => {
    const base = resource('a', settings(15, 600))
    const latestSettings = settings(30, 600)
    latestSettings.overrides = ['connect_timeout']
    const latest = resource('b', latestSettings)
    const draft = createSettingsDraft(base.settings)
    draft.overrides.add('request_timeout')
    draft.values.request_timeout = 900

    const result = mergeSettingsConflict(base, draft, latest, 'request-forwarding')

    expect(result.conflicts).toEqual([])
    expect(result.resource).toBe(latest)
    expect(result.draft.values.connect_timeout).toBe(30)
    expect(result.draft.values.request_timeout).toBe(900)
    expect(result.draft.overrides).toEqual(new Set(['connect_timeout', 'request_timeout']))
  })

  it('detects the same-field conflict as override plus normalized value identity', () => {
    const baseSettings = settings(15)
    baseSettings.overrides = ['connect_timeout']
    const base = resource('a', baseSettings)
    const latestSettings = settings(45)
    latestSettings.overrides = ['connect_timeout']
    const latest = resource('b', latestSettings)
    const draft = createSettingsDraft(base.settings)
    draft.values.connect_timeout = 30

    const result = mergeSettingsConflict(base, draft, latest, 'request-forwarding')

    expect(result.conflicts).toEqual([
      {
        key: 'connect_timeout',
        mine: { is_override: true, normalized_value: 30 },
        latest: { is_override: true, normalized_value: 45 },
      },
    ])
  })

  it('uses canonical HeaderRules deep equality for case and ordering', () => {
    const baseSettings = settings(15)
    baseSettings.values.header_rules = {
      set: { 'X-Alpha': 'one', 'X-Beta': 'two' },
      remove: ['X-Remove'],
    }
    baseSettings.overrides = ['header_rules']
    const base = resource('a', baseSettings)
    const latestSettings = structuredClone(baseSettings)
    latestSettings.values.header_rules = {
      set: { 'x-beta': 'two', 'x-alpha': 'one' },
      remove: ['x-remove'],
    }
    const latest = resource('b', latestSettings)
    const draft = createSettingsDraft(base.settings)

    expect(mergeSettingsConflict(base, draft, latest, 'request-forwarding').conflicts).toEqual([])
  })

  it('confirms an unknown write only when latest contains every intended field identity', () => {
    const base = resource('a', settings(15))
    const draft = createSettingsDraft(base.settings)
    draft.overrides.add('connect_timeout')
    draft.values.connect_timeout = 30
    const appliedSettings = settings(30)
    appliedSettings.overrides = ['connect_timeout']

    expect(
      reconcileSettingsMutation(base, draft, resource('b', appliedSettings), 'request-forwarding')
        .kind,
    ).toBe('confirmed')
    expect(
      reconcileSettingsMutation(base, draft, resource('a', settings(15)), 'request-forwarding')
        .kind,
    ).toBe('indeterminate')
  })

  it('turns a divergent latest value into an explicit conflict during unknown-write reconciliation', () => {
    const baseSettings = settings(15)
    baseSettings.overrides = ['connect_timeout']
    const base = resource('a', baseSettings)
    const draft = createSettingsDraft(base.settings)
    draft.values.connect_timeout = 30
    const latestSettings = settings(45)
    latestSettings.overrides = ['connect_timeout']

    const result = reconcileSettingsMutation(
      base,
      draft,
      resource('b', latestSettings),
      'request-forwarding',
    )

    expect(result.kind).toBe('conflict')
    expect(result.conflicts).toHaveLength(1)
  })
})
