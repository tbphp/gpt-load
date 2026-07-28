import { runtimeSettingKeys, type SettingsDto } from '@/api/control/settings'

import {
  buildSettingsPatch,
  createSettingsDraft,
  hasDuplicateHeaderNames,
  rebaseSettingsDraft,
  settingsScopeKeys,
  setSettingsOverride,
  validateSettingsSection,
} from './settings-patch'

const base: SettingsDto = {
  values: {
    connect_timeout: 15,
    first_byte_timeout: 120,
    request_timeout: 600,
    stream_idle_timeout: 300,
    header_rules: { set: { 'X-Base': 'secret' }, remove: ['X-Remove'] },
    inject_usage_options: false,
    request_log_retention_days: 7,
  },
  overrides: ['request_timeout', 'header_rules'],
}

describe('settings patching', () => {
  it('enumerates every runtime setting exactly once for the all scope', () => {
    const keys = settingsScopeKeys('all')

    expect(keys).toEqual(runtimeSettingKeys)
    expect(new Set(keys).size).toBe(runtimeSettingKeys.length)
  })

  it('builds and rebases one normalized patch across both sections', () => {
    const draft = createSettingsDraft(base)
    draft.values.request_timeout = 900
    draft.values.request_log_retention_days = 30
    draft.overrides.add('request_log_retention_days')

    expect(buildSettingsPatch(base, draft, 'all')).toEqual({
      request_timeout: 900,
      request_log_retention_days: 30,
    })

    const refreshed: SettingsDto = {
      values: { ...base.values, connect_timeout: 45 },
      overrides: [...base.overrides, 'connect_timeout'],
    }
    const rebased = rebaseSettingsDraft(base, draft, refreshed, 'all')

    expect(buildSettingsPatch(refreshed, rebased, 'all')).toEqual({
      request_timeout: 900,
      request_log_retention_days: 30,
    })
    expect(rebased.values.connect_timeout).toBe(45)
  })

  it('builds a normalized dirty patch independently for request forwarding', () => {
    const draft = createSettingsDraft(base)
    draft.values.request_timeout = 900
    draft.overrides.delete('header_rules')

    expect(buildSettingsPatch(base, draft, 'request-forwarding')).toEqual({
      request_timeout: 900,
      header_rules: null,
    })
    expect(buildSettingsPatch(base, draft, 'logs-maintenance')).toEqual({})
  })

  it('returns an empty patch for normalized unchanged values', () => {
    const draft = createSettingsDraft(base)
    draft.values.header_rules = {
      set: { ' X-Base ': 'secret' },
      remove: [' X-Remove ', 'X-Remove'],
    }

    expect(buildSettingsPatch(base, draft, 'request-forwarding')).toEqual({})
    expect(buildSettingsPatch(base, draft, 'logs-maintenance')).toEqual({})
  })

  it('seeds the current effective value when enabling ownership and sends null on reset', () => {
    const inherited = { ...base, overrides: [] }
    let draft = createSettingsDraft(inherited)
    draft.values.connect_timeout = 999
    draft = setSettingsOverride(inherited, draft, 'connect_timeout', true)

    expect(draft.values.connect_timeout).toBe(15)
    expect(buildSettingsPatch(inherited, draft, 'request-forwarding')).toEqual({
      connect_timeout: 15,
    })

    draft = createSettingsDraft(base)
    draft = setSettingsOverride(base, draft, 'request_timeout', false)
    expect(buildSettingsPatch(base, draft, 'request-forwarding')).toEqual({
      request_timeout: null,
    })
  })

  it('accepts only positive safe-integer timeouts and retention from 1 through 365', () => {
    const draft = createSettingsDraft(base)
    draft.overrides.add('connect_timeout')
    draft.overrides.add('request_log_retention_days')
    for (const value of [0, -1, 1.5, Number.MAX_SAFE_INTEGER + 1]) {
      draft.values.connect_timeout = value
      expect(validateSettingsSection(draft, 'request-forwarding')).toBe(false)
    }
    draft.values.connect_timeout = Number.MAX_SAFE_INTEGER
    expect(validateSettingsSection(draft, 'request-forwarding')).toBe(true)

    for (const value of [0, 366, 1.5, Number.MAX_SAFE_INTEGER + 1]) {
      draft.values.request_log_retention_days = value
      expect(validateSettingsSection(draft, 'logs-maintenance')).toBe(false)
    }
    for (const value of [1, 365]) {
      draft.values.request_log_retention_days = value
      expect(validateSettingsSection(draft, 'logs-maintenance')).toBe(true)
    }
  })

  it('detects HeaderRules duplicates case-insensitively without exposing values', () => {
    expect(hasDuplicateHeaderNames({ set: { 'X-Test': 'one' }, remove: ['x-test'] })).toBe(true)
    expect(hasDuplicateHeaderNames({ set: { 'X-Test': 'one' }, remove: ['X-Other'] })).toBe(false)
  })

  it('replays only the current section dirty patch onto refreshed settings', () => {
    const draft = createSettingsDraft(base)
    draft.values.connect_timeout = 30
    draft.overrides.add('connect_timeout')
    const refreshed: SettingsDto = {
      ...base,
      overrides: ['request_timeout', 'header_rules'],
      values: { ...base.values, request_timeout: 900 },
    }

    const rebased = rebaseSettingsDraft(base, draft, refreshed, 'request-forwarding')

    expect(buildSettingsPatch(refreshed, rebased, 'request-forwarding')).toEqual({
      connect_timeout: 30,
    })
    expect(rebased.values.request_timeout).toBe(900)
    expect(rebased.overrides.has('request_timeout')).toBe(true)
  })

  it('keeps the hidden system boolean through clone patch and rebase', () => {
    const draft = createSettingsDraft(base)
    draft.values.connect_timeout = 30
    draft.overrides.add('connect_timeout')
    const refreshed: SettingsDto = {
      ...base,
      values: { ...base.values, inject_usage_options: true },
      overrides: [...base.overrides],
    }

    const rebased = rebaseSettingsDraft(base, draft, refreshed, 'request-forwarding')

    expect(rebased.values.inject_usage_options).toBe(true)
    expect(buildSettingsPatch(refreshed, rebased, 'request-forwarding')).toEqual({
      connect_timeout: 30,
    })
  })
})
