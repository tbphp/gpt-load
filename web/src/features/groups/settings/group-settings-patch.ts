import type {
  ChannelParamsDto,
  GroupSettingsDto,
  ParameterJSONValue,
  ParameterOverrideRuleDto,
} from '@/api/control/types'
import type {
  GroupRuntimeConfigDto,
  GroupSettingsUpdateRequest,
  HeaderRulesDto,
} from '@/app/resources/groups'

export type GroupTimeoutKey = 'first_byte_timeout' | 'request_timeout' | 'stream_idle_timeout'
export type GroupPolicyCountKey = 'retry_count' | 'blacklist_threshold'

export interface GroupSettingsDraft {
  channel_id: string
  connection_type: GroupSettingsDto['connection_type']
  params: ChannelParamsDto
  name: string
  validation_model: string | null
  enabled: boolean
  weight_manual: number | null
  overrides: GroupRuntimeConfigDto
}

export const groupTimeoutKeys: readonly GroupTimeoutKey[] = [
  'first_byte_timeout',
  'request_timeout',
  'stream_idle_timeout',
]
export const groupPolicyCountKeys: readonly GroupPolicyCountKey[] = [
  'retry_count',
  'blacklist_threshold',
]

function cloneHeaders(value: HeaderRulesDto): HeaderRulesDto {
  return { set: { ...value.set }, remove: [...value.remove] }
}

function cloneParameterValue(value: unknown): ParameterJSONValue {
  if (Array.isArray(value)) return value.map(cloneParameterValue)
  if (value !== null && typeof value === 'object') {
    return Object.fromEntries(
      Object.entries(value).map(([key, nested]) => [key, cloneParameterValue(nested)]),
    )
  }
  if (
    value === null ||
    typeof value === 'boolean' ||
    typeof value === 'number' ||
    typeof value === 'string'
  )
    return value
  throw new TypeError('invalid JSON value')
}

function cloneParameterOverrides(value: ParameterOverrideRuleDto[]): ParameterOverrideRuleDto[] {
  return value.map((rule) => ({
    match: { ...rule.match },
    ...(rule.set
      ? {
          set: Object.fromEntries(
            Object.entries(rule.set).map(([key, nested]) => [key, cloneParameterValue(nested)]),
          ),
        }
      : {}),
    ...(rule.remove ? { remove: [...rule.remove] } : {}),
  }))
}

function cloneOverrides(value: GroupRuntimeConfigDto): GroupRuntimeConfigDto {
  const next: GroupRuntimeConfigDto = {}
  for (const key of groupTimeoutKeys) if (value[key] !== undefined) next[key] = value[key]
  for (const key of groupPolicyCountKeys) if (value[key] !== undefined) next[key] = value[key]
  if (value.header_rules) next.header_rules = cloneHeaders(value.header_rules)
  if (value.inject_usage_options !== undefined)
    next.inject_usage_options = value.inject_usage_options
  if (value.affinity_enabled !== undefined) next.affinity_enabled = value.affinity_enabled
  if (value.parameter_overrides?.length)
    next.parameter_overrides = cloneParameterOverrides(value.parameter_overrides)
  return next
}

function normalizeHeaders(value: HeaderRulesDto): HeaderRulesDto {
  return {
    set: Object.fromEntries(
      Object.entries(value.set).sort(([left], [right]) => left.localeCompare(right)),
    ),
    remove: [...value.remove].sort(),
  }
}

function normalizeOverrides(value: GroupRuntimeConfigDto): GroupRuntimeConfigDto {
  const next = cloneOverrides(value)
  if (next.header_rules) next.header_rules = normalizeHeaders(next.header_rules)
  if (next.parameter_overrides) {
    const rules = next.parameter_overrides.map((rule) => {
      const model = rule.match.model?.trim()
      const match = {
        ...(rule.match.protocol ? { protocol: rule.match.protocol } : {}),
        ...(model ? { model } : {}),
      }
      const set = rule.set ? normalizeParameterObject(rule.set) : undefined
      return {
        match,
        ...(set && Object.keys(set).length > 0 ? { set } : {}),
        ...(rule.remove && rule.remove.length > 0 ? { remove: [...rule.remove] } : {}),
      }
    })
    if (rules.length > 0) next.parameter_overrides = rules
    else delete next.parameter_overrides
  }
  return next
}

function normalizeParameterObject(
  value: Record<string, unknown>,
): Record<string, ParameterJSONValue> {
  return Object.fromEntries(
    Object.entries(value)
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([key, nested]) => [key, normalizeParameterValue(nested)]),
  )
}

function normalizeParameterValue(value: unknown): ParameterJSONValue {
  if (Array.isArray(value)) return value.map(normalizeParameterValue)
  if (value !== null && typeof value === 'object')
    return normalizeParameterObject(value as Record<string, unknown>)
  if (
    value === null ||
    typeof value === 'boolean' ||
    typeof value === 'number' ||
    typeof value === 'string'
  )
    return value
  throw new TypeError('invalid JSON value')
}

export function createGroupSettingsDraft(group: GroupSettingsDto): GroupSettingsDraft {
  return { ...group, params: { ...group.params }, overrides: cloneOverrides(group.overrides) }
}

export function setGroupConfigOverride(
  draft: GroupSettingsDraft,
  key: GroupTimeoutKey,
  enabled: boolean,
  effective: number,
): GroupSettingsDraft {
  const overrides = cloneOverrides(draft.overrides)
  if (enabled) overrides[key] = effective
  else delete overrides[key]
  return { ...draft, overrides }
}

export function setGroupPolicyCountOverride(
  draft: GroupSettingsDraft,
  key: GroupPolicyCountKey,
  enabled: boolean,
  effective: number,
): GroupSettingsDraft {
  const overrides = cloneOverrides(draft.overrides)
  if (enabled) overrides[key] = effective
  else delete overrides[key]
  return { ...draft, overrides }
}

export function buildGroupSettingsPatch(
  base: GroupSettingsDto,
  draft: GroupSettingsDraft,
): GroupSettingsUpdateRequest {
  const patch: GroupSettingsUpdateRequest = {}
  const overrides = normalizeOverrides(draft.overrides)
  if (draft.name.trim() !== base.name) patch.name = draft.name.trim()
  const params = Object.fromEntries(
    Object.entries(draft.params)
      .map(([key, value]) => [key, value.trim()] as const)
      .sort(([left], [right]) => left.localeCompare(right)),
  )
  const baseParams = Object.fromEntries(
    Object.entries(base.params).sort(([left], [right]) => left.localeCompare(right)),
  )
  if (JSON.stringify(params) !== JSON.stringify(baseParams)) patch.params = params
  const validationModel = draft.validation_model?.trim() || null
  if (validationModel !== base.validation_model) patch.validation_model = validationModel
  if (draft.enabled !== base.enabled) patch.enabled = draft.enabled
  if (draft.weight_manual !== base.weight_manual) patch.weight_manual = draft.weight_manual
  if (JSON.stringify(overrides) !== JSON.stringify(normalizeOverrides(base.overrides))) {
    patch.overrides = overrides
  }
  return patch
}
