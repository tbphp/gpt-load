import type { ChannelParamsDto, GroupSettingsDto } from '@/api/control/types'
import type {
  GroupRuntimeConfigDto,
  GroupSettingsUpdateRequest,
  HeaderRulesDto,
} from '@/app/resources/groups'

export type GroupTimeoutKey = 'first_byte_timeout' | 'request_timeout' | 'stream_idle_timeout'

export interface GroupSettingsDraft {
  channel_id: string
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

function cloneHeaders(value: HeaderRulesDto): HeaderRulesDto {
  return { set: { ...value.set }, remove: [...value.remove] }
}

function cloneOverrides(value: GroupRuntimeConfigDto): GroupRuntimeConfigDto {
  const next: GroupRuntimeConfigDto = {}
  for (const key of groupTimeoutKeys) if (value[key] !== undefined) next[key] = value[key]
  if (value.header_rules) next.header_rules = cloneHeaders(value.header_rules)
  if (value.inject_usage_options !== undefined)
    next.inject_usage_options = value.inject_usage_options
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
  return next
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
