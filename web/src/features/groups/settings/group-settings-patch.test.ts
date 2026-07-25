import type { GroupDetailDto } from '@/api/control/groups'

import {
  buildGroupSettingsPatch,
  createGroupSettingsDraft,
  enableHeaderRulesOverride,
  setGroupConfigOverride,
} from './group-settings-patch'

const base: GroupDetailDto = {
  id: 7,
  name: 'Primary',
  upstream_url: 'https://api.example.com/v1',
  protocols: ['openai'],
  models: [{ id: 'gpt-4o', alias: '' }],
  enabled: true,
  key_count: 2,
  validation_model: 'gpt-4o',
  weight_manual: null,
  config: {
    connect_timeout: 30,
    header_rules: { set: { 'X-Token': 'HEADER_CANARY_PATCH' }, remove: ['X-Debug'] },
  },
  effective_config: {
    connect_timeout: 30,
    first_byte_timeout: 120,
    request_timeout: 600,
    stream_idle_timeout: 300,
    header_rules: { set: { 'X-Token': 'HEADER_CANARY_PATCH' }, remove: ['X-Debug'] },
  },
}

describe('group settings patch', () => {
  it('returns an empty normalized patch when the draft is unchanged', () => {
    const draft = createGroupSettingsDraft(base)
    draft.name = ' Primary '
    draft.upstream_url = ' https://api.example.com/v1 '
    draft.validation_model = ' gpt-4o '

    expect(buildGroupSettingsPatch(base, draft)).toEqual({})
  })

  it('preserves explicit nullable base fields and sends only changed base fields', () => {
    const draft = createGroupSettingsDraft(base)
    draft.validation_model = null

    expect(buildGroupSettingsPatch(base, draft)).toEqual({ validation_model: null })
  })

  it('normalizes and includes all six supported base fields without unrelated data', () => {
    const draft = createGroupSettingsDraft(base)
    draft.name = ' Renamed '
    draft.enabled = false
    draft.upstream_url = ' https://new.example.com/v1 '
    draft.protocols = ['gemini', 'openai', 'gemini']
    draft.validation_model = ' gemini-2.5-pro '
    draft.weight_manual = 42

    expect(buildGroupSettingsPatch(base, draft)).toEqual({
      name: 'Renamed',
      enabled: false,
      upstream_url: 'https://new.example.com/v1',
      protocols: ['openai', 'gemini'],
      validation_model: 'gemini-2.5-pro',
      weight_manual: 42,
    })
  })

  it('owns exactly the five Group runtime keys and sends the complete resulting sparse config', () => {
    let draft = createGroupSettingsDraft(base)
    draft = setGroupConfigOverride(draft, 'connect_timeout', false, base.effective_config)
    draft = setGroupConfigOverride(draft, 'first_byte_timeout', true, base.effective_config)
    draft = setGroupConfigOverride(draft, 'request_timeout', true, base.effective_config)
    draft = setGroupConfigOverride(draft, 'stream_idle_timeout', true, base.effective_config)
    draft.config.header_rules = { set: { 'X-New': 'HEADER_CANARY_NEW' }, remove: [] }

    const patch = buildGroupSettingsPatch(base, draft)
    expect(patch).toEqual({
      config: {
        first_byte_timeout: 120,
        request_timeout: 600,
        stream_idle_timeout: 300,
        header_rules: { set: { 'X-New': 'HEADER_CANARY_NEW' }, remove: [] },
      },
    })
    expect(JSON.stringify(patch)).not.toContain('effective_config')
    expect(JSON.stringify(patch)).not.toContain('request_log_retention_days')
  })

  it('enables HeaderRules from a deep clone of the effective value', () => {
    const enabled = enableHeaderRulesOverride(base.effective_config.header_rules)

    expect(enabled).toEqual(base.effective_config.header_rules)
    expect(enabled).not.toBe(base.effective_config.header_rules)
    expect(enabled.set).not.toBe(base.effective_config.header_rules.set)
    expect(enabled.remove).not.toBe(base.effective_config.header_rules.remove)
  })
})
