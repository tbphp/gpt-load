import type {
  GroupDetailDto,
  GroupEffectiveConfigDto,
  GroupRuntimeConfigDto,
  GroupUpdateRequest,
  HeaderRulesDto,
} from '@/app/resources/groups'
import type { GroupProtocol } from '@/api/control/types'

export type GroupTimeoutKey =
  'connect_timeout' | 'first_byte_timeout' | 'request_timeout' | 'stream_idle_timeout'

export interface GroupSettingsDraft {
  name: string
  enabled: boolean
  upstream_url: string
  protocols: GroupProtocol[]
  validation_model: string | null
  weight_manual: number | null
  config: GroupRuntimeConfigDto
}

const protocolOrder: GroupProtocol[] = ['openai', 'anthropic', 'gemini']

function cloneHeaderRules(value: HeaderRulesDto): HeaderRulesDto {
  return { set: { ...value.set }, remove: [...value.remove] }
}

function cloneConfig(value: GroupRuntimeConfigDto): GroupRuntimeConfigDto {
  const config: GroupRuntimeConfigDto = {}
  if (value.connect_timeout !== undefined) config.connect_timeout = value.connect_timeout
  if (value.first_byte_timeout !== undefined) config.first_byte_timeout = value.first_byte_timeout
  if (value.request_timeout !== undefined) config.request_timeout = value.request_timeout
  if (value.stream_idle_timeout !== undefined)
    config.stream_idle_timeout = value.stream_idle_timeout
  if (value.header_rules !== undefined) config.header_rules = cloneHeaderRules(value.header_rules)
  if (value.inject_usage_options !== undefined)
    config.inject_usage_options = value.inject_usage_options
  return config
}

function normalizeGroupProtocols(value: readonly GroupProtocol[]): GroupProtocol[] {
  const selected = new Set(value)
  return protocolOrder.filter((protocol) => selected.has(protocol))
}

function normalizeHeaderRules(value: HeaderRulesDto): HeaderRulesDto {
  const set = Object.fromEntries(
    Object.entries(value.set)
      .map(([name, headerValue]) => [name.trim(), headerValue] as const)
      .filter(([name]) => name !== '')
      .sort(([left], [right]) => (left < right ? -1 : left > right ? 1 : 0)),
  )
  const remove = [...new Set(value.remove.map((name) => name.trim()).filter(Boolean))].sort()
  return { set, remove }
}

function normalizeConfig(value: GroupRuntimeConfigDto): GroupRuntimeConfigDto {
  const config: GroupRuntimeConfigDto = {}
  if (value.connect_timeout !== undefined) config.connect_timeout = value.connect_timeout
  if (value.first_byte_timeout !== undefined) config.first_byte_timeout = value.first_byte_timeout
  if (value.request_timeout !== undefined) config.request_timeout = value.request_timeout
  if (value.stream_idle_timeout !== undefined)
    config.stream_idle_timeout = value.stream_idle_timeout
  if (value.header_rules !== undefined)
    config.header_rules = normalizeHeaderRules(value.header_rules)
  if (value.inject_usage_options !== undefined)
    config.inject_usage_options = value.inject_usage_options
  return config
}

function sameValue(left: unknown, right: unknown): boolean {
  return JSON.stringify(left) === JSON.stringify(right)
}

export function createGroupSettingsDraft(group: GroupDetailDto): GroupSettingsDraft {
  return {
    name: group.name,
    enabled: group.enabled,
    upstream_url: group.upstream_url,
    protocols: [...group.protocols],
    validation_model: group.validation_model,
    weight_manual: group.weight_manual,
    config: cloneConfig(group.config),
  }
}

export function enableHeaderRulesOverride(value: HeaderRulesDto): HeaderRulesDto {
  return cloneHeaderRules(value)
}

export function setGroupConfigOverride(
  draft: GroupSettingsDraft,
  key: GroupTimeoutKey,
  enabled: boolean,
  effective: GroupEffectiveConfigDto,
): GroupSettingsDraft {
  const config = cloneConfig(draft.config)
  if (enabled) config[key] = effective[key]
  else delete config[key]
  return { ...draft, config }
}

export function buildGroupSettingsPatch(
  base: GroupDetailDto,
  draft: GroupSettingsDraft,
): GroupUpdateRequest {
  const patch: GroupUpdateRequest = {}
  const name = draft.name.trim()
  const upstreamURL = draft.upstream_url.trim()
  const protocols = normalizeGroupProtocols(draft.protocols)
  const validationModel = draft.validation_model?.trim() || null
  const config = normalizeConfig(draft.config)
  const baseConfig = normalizeConfig(base.config)

  if (name !== base.name) patch.name = name
  if (draft.enabled !== base.enabled) patch.enabled = draft.enabled
  if (upstreamURL !== base.upstream_url) patch.upstream_url = upstreamURL
  if (!sameValue(protocols, normalizeGroupProtocols(base.protocols))) patch.protocols = protocols
  if (validationModel !== base.validation_model) patch.validation_model = validationModel
  if (draft.weight_manual !== base.weight_manual) patch.weight_manual = draft.weight_manual
  if (!sameValue(config, baseConfig)) patch.config = config

  return patch
}

export function rebaseGroupSettingsDraft(
  base: GroupDetailDto,
  draft: GroupSettingsDraft,
  refreshed: GroupDetailDto,
): GroupSettingsDraft {
  const patch = buildGroupSettingsPatch(base, draft)
  const rebased = createGroupSettingsDraft(refreshed)

  if (patch.name !== undefined) rebased.name = draft.name
  if (patch.enabled !== undefined) rebased.enabled = draft.enabled
  if (patch.upstream_url !== undefined) rebased.upstream_url = draft.upstream_url
  if (patch.protocols !== undefined) rebased.protocols = [...draft.protocols]
  if ('validation_model' in patch) rebased.validation_model = draft.validation_model
  if ('weight_manual' in patch) rebased.weight_manual = draft.weight_manual

  const timeoutKeys: GroupTimeoutKey[] = [
    'connect_timeout',
    'first_byte_timeout',
    'request_timeout',
    'stream_idle_timeout',
  ]
  for (const key of timeoutKeys) {
    if (base.config[key] === draft.config[key]) continue
    const value = draft.config[key]
    if (value === undefined) delete rebased.config[key]
    else rebased.config[key] = value
  }
  const baseHeaders =
    base.config.header_rules === undefined
      ? undefined
      : normalizeHeaderRules(base.config.header_rules)
  const draftHeaders =
    draft.config.header_rules === undefined
      ? undefined
      : normalizeHeaderRules(draft.config.header_rules)
  if (!sameValue(baseHeaders, draftHeaders)) {
    if (draft.config.header_rules === undefined) delete rebased.config.header_rules
    else rebased.config.header_rules = cloneHeaderRules(draft.config.header_rules)
  }

  if (base.config.inject_usage_options !== draft.config.inject_usage_options) {
    if (draft.config.inject_usage_options === undefined) delete rebased.config.inject_usage_options
    else rebased.config.inject_usage_options = draft.config.inject_usage_options
  }

  return rebased
}
