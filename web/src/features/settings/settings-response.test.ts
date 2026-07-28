import type { SettingsDto } from '@/api/control/settings'

import { chooseSettingsMutationResult } from './settings-response'
import { createSettingsDraft } from './settings-patch'

function settings(revision: number): SettingsDto {
  return {
    revision,
    values: {
      connect_timeout: revision * 10,
      first_byte_timeout: revision * 20,
      request_timeout: revision * 30,
      stream_idle_timeout: revision * 40,
      header_rules: { set: {}, remove: [] },
      inject_usage_options: false,
      request_log_retention_days: 7,
    },
    overrides: [],
  }
}

describe('chooseSettingsMutationResult', () => {
  it.each([
    ['newer response', 12, 11, 'response'],
    ['equal response', 11, 11, 'response'],
    ['stale response', 10, 11, 'cache'],
  ] as const)(
    '%s selects the monotonic source and rebases the dirty draft',
    (_name, responseRevision, cacheRevision, source) => {
      const response = settings(responseRevision)
      const cached = settings(cacheRevision)
      const base = settings(9)
      const draft = createSettingsDraft(base)
      draft.overrides.add('connect_timeout')
      draft.values.connect_timeout = 4321
      const draftSnapshot = {
        values: structuredClone(draft.values),
        overrides: new Set(draft.overrides),
      }

      const result = chooseSettingsMutationResult(
        response,
        cached,
        base,
        draft,
        'request-forwarding',
      )

      expect(result.kind).toBe('apply')
      if (result.kind !== 'apply') return
      expect(result.source).toBe(source)
      expect(result.settings).toBe(source === 'response' ? response : cached)
      expect(result.draft).not.toBe(draft)
      expect(result.draft.values.connect_timeout).toBe(draft.values.connect_timeout)
      expect(result.draft.values.first_byte_timeout).toBe(result.settings.values.first_byte_timeout)
      expect(draft).toEqual(draftSnapshot)
    },
  )

  it('requires a refetch when cache presence cannot be confirmed', () => {
    const base = settings(9)

    expect(
      chooseSettingsMutationResult(
        settings(10),
        undefined,
        base,
        createSettingsDraft(base),
        'request-forwarding',
      ),
    ).toEqual({ kind: 'refetch' })
  })
})
