import type { SettingsDto } from '@/api/control/settings'

import {
  buildSettingsPatch,
  createSettingsDraft,
  hasDuplicateHeaderNames,
  setSettingsOverride,
  validateSettingsSection,
} from './settings-patch'

const base: SettingsDto = {
  revision: 3,
  values: {
    connect_timeout: 15,
    first_byte_timeout: 120,
    request_timeout: 600,
    stream_idle_timeout: 300,
    header_rules: { set: { 'X-Base': 'secret' }, remove: ['X-Remove'] },
    request_log_retention_days: 7,
  },
  overrides: ['request_timeout', 'header_rules'],
}

describe('settings patching', () => {
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
})
