import type { GroupProtocol, GroupSettingsDto } from '@/api/control/types'
import type {
  GroupRuntimeConfigDto,
  GroupSettingsUpdateRequest,
  HeaderRulesDto,
} from '@/app/resources/groups'
import { enabledDataProtocols } from '@/api/control/protocols'

export type GroupTimeoutKey =
  'connect_timeout' | 'first_byte_timeout' | 'request_timeout' | 'stream_idle_timeout'

export interface GroupSettingsDraft {
  name: string
  upstream_url: string
  protocols: GroupProtocol[]
  validation_model: string | null
  enabled: boolean
  weight_manual: number | null
  overrides: GroupRuntimeConfigDto
}

const timeoutKeys: GroupTimeoutKey[] = [
  'connect_timeout',
  'first_byte_timeout',
  'request_timeout',
  'stream_idle_timeout',
]

function cloneHeaders(value: HeaderRulesDto): HeaderRulesDto {
  return { set: { ...value.set }, remove: [...value.remove] }
}

function cloneOverrides(value: GroupRuntimeConfigDto): GroupRuntimeConfigDto {
  const next: GroupRuntimeConfigDto = {}
  for (const key of timeoutKeys) if (value[key] !== undefined) next[key] = value[key]
  if (value.header_rules) next.header_rules = cloneHeaders(value.header_rules)
  if (value.inject_usage_options !== undefined)
    next.inject_usage_options = value.inject_usage_options
  return next
}

function normalizeHeaders(value: HeaderRulesDto): HeaderRulesDto {
  return {
    set: Object.fromEntries(
      Object.entries(value.set)
        .map(([name, headerValue]) => [name.trim(), headerValue] as const)
        .filter(([name]) => name)
        .sort(([left], [right]) => left.localeCompare(right)),
    ),
    remove: [...new Set(value.remove.map((name) => name.trim()).filter(Boolean))].sort(),
  }
}

function normalizeOverrides(value: GroupRuntimeConfigDto): GroupRuntimeConfigDto {
  const next = cloneOverrides(value)
  if (next.header_rules) next.header_rules = normalizeHeaders(next.header_rules)
  return next
}

export function createGroupSettingsDraft(group: GroupSettingsDto): GroupSettingsDraft {
  return { ...group, protocols: [...group.protocols], overrides: cloneOverrides(group.overrides) }
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
  const protocols = enabledDataProtocols.filter((protocol) => draft.protocols.includes(protocol))
  const overrides = normalizeOverrides(draft.overrides)
  if (!protocols.includes('openai-completions')) delete overrides.inject_usage_options
  if (draft.name.trim() !== base.name) patch.name = draft.name.trim()
  if (draft.upstream_url.trim() !== base.upstream_url)
    patch.upstream_url = draft.upstream_url.trim()
  if (JSON.stringify(protocols) !== JSON.stringify(base.protocols)) patch.protocols = protocols
  const validationModel = draft.validation_model?.trim() || null
  if (validationModel !== base.validation_model) patch.validation_model = validationModel
  if (draft.enabled !== base.enabled) patch.enabled = draft.enabled
  if (draft.weight_manual !== base.weight_manual) patch.weight_manual = draft.weight_manual
  if (JSON.stringify(overrides) !== JSON.stringify(normalizeOverrides(base.overrides))) {
    patch.overrides = overrides
  }
  return patch
}
