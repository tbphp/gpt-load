import type { HeaderRulesDto } from '@/app/resources/groups'
import type {
  RuntimeSettingKey,
  PolicyCountSettingKey,
  SettingsDto,
  SettingsPatch,
  SettingsValues,
  TimeoutSettingKey,
} from '@/app/resources/settings'
import { runtimeSettingKeys } from '@/app/resources/settings'

export type SettingsSection =
  'request-forwarding' | 'affinity' | 'logs-maintenance' | 'model-prices'
export type SettingsScope = SettingsSection | 'all'

export interface SettingsDraft {
  values: SettingsValues
  overrides: Set<RuntimeSettingKey>
  readOnly: Set<RuntimeSettingKey>
}

export interface SettingsFieldIdentity {
  is_override: boolean
  is_read_only: boolean
  normalized_value: number | boolean | HeaderRulesDto
}

const requestForwardingKeys: RuntimeSettingKey[] = [
  'first_byte_timeout',
  'request_timeout',
  'stream_idle_timeout',
  'retry_count',
  'blacklist_threshold',
  'header_rules',
  'inject_usage_options',
  'validation_interval',
]
const logsMaintenanceKeys: RuntimeSettingKey[] = ['request_log_retention_days']
const affinityKeys: RuntimeSettingKey[] = ['affinity_enabled', 'affinity_ttl', 'affinity_capacity']
const modelPriceKeys: RuntimeSettingKey[] = ['models_dev_auto_sync_enabled']

function cloneHeaderRules(value: HeaderRulesDto): HeaderRulesDto {
  return { set: { ...value.set }, remove: [...value.remove] }
}

function cloneValues(value: SettingsValues): SettingsValues {
  return { ...value, header_rules: cloneHeaderRules(value.header_rules) }
}

export function createSettingsDraft(settings: SettingsDto): SettingsDraft {
  return {
    values: cloneValues(settings.values),
    overrides: new Set(settings.overrides),
    readOnly: new Set(settings.read_only),
  }
}

export function setSettingsOverride(
  base: SettingsDto,
  draft: SettingsDraft,
  key: RuntimeSettingKey,
  enabled: boolean,
): SettingsDraft {
  const next: SettingsDraft = {
    values: cloneValues(draft.values),
    overrides: new Set(draft.overrides),
    readOnly: new Set(draft.readOnly),
  }
  if (next.readOnly.has(key)) return next
  if (enabled) {
    next.overrides.add(key)
    if (key === 'inject_usage_options') {
      next.values.inject_usage_options = base.values.inject_usage_options
    } else if (key === 'affinity_enabled') {
      next.values.affinity_enabled = base.values.affinity_enabled
    } else if (key === 'models_dev_auto_sync_enabled') {
      next.values.models_dev_auto_sync_enabled = base.values.models_dev_auto_sync_enabled
    } else if (key !== 'header_rules') {
      next.values[key] = base.values[key]
    }
  } else {
    next.overrides.delete(key)
    if (key === 'header_rules') next.values.header_rules = { set: {}, remove: [] }
  }
  return next
}

function normalizeHeaderRules(value: HeaderRulesDto): HeaderRulesDto {
  const set = Object.fromEntries(
    Object.entries(value.set).sort(([left], [right]) => left.localeCompare(right)),
  )
  const remove = [...value.remove].sort((a, b) => a.localeCompare(b))
  return { set, remove }
}

function normalizedWireValue(
  settings: SettingsValues,
  key: RuntimeSettingKey,
): number | boolean | HeaderRulesDto {
  if (key === 'header_rules') return normalizeHeaderRules(settings.header_rules)
  return settings[key]
}

function canonicalHeaderRulesIdentity(value: HeaderRulesDto): HeaderRulesDto {
  const set = Object.fromEntries(
    Object.entries(value.set)
      .map(([name, headerValue]) => [asciiLower(name), headerValue] as const)
      .sort(([left], [right]) => left.localeCompare(right)),
  )
  const remove = value.remove
    .map((name) => asciiLower(name))
    .sort((left, right) => left.localeCompare(right))
  return { set, remove }
}

function normalizedIdentityValue(
  settings: SettingsValues,
  key: RuntimeSettingKey,
): number | boolean | HeaderRulesDto {
  if (key === 'header_rules') return canonicalHeaderRulesIdentity(settings.header_rules)
  return normalizedWireValue(settings, key)
}

function sameValue(left: unknown, right: unknown): boolean {
  return JSON.stringify(left) === JSON.stringify(right)
}

export function settingsSectionKeys(section: SettingsSection): RuntimeSettingKey[] {
  if (section === 'request-forwarding') return [...requestForwardingKeys]
  if (section === 'affinity') return [...affinityKeys]
  if (section === 'logs-maintenance') return [...logsMaintenanceKeys]
  return [...modelPriceKeys]
}

export function settingsScopeKeys(scope: SettingsScope): RuntimeSettingKey[] {
  return scope === 'all' ? [...runtimeSettingKeys] : settingsSectionKeys(scope)
}

export function settingsFieldIdentity(
  settings: SettingsDto,
  key: RuntimeSettingKey,
): SettingsFieldIdentity {
  return {
    is_override: settings.overrides.includes(key),
    is_read_only: settings.read_only.includes(key),
    normalized_value: normalizedIdentityValue(settings.values, key),
  }
}

export function draftFieldIdentity(
  draft: SettingsDraft,
  key: RuntimeSettingKey,
): SettingsFieldIdentity {
  return {
    is_override: draft.overrides.has(key),
    is_read_only: draft.readOnly.has(key),
    normalized_value: normalizedIdentityValue(draft.values, key),
  }
}

export function sameSettingsFieldIdentity(
  left: SettingsFieldIdentity,
  right: SettingsFieldIdentity,
): boolean {
  return (
    left.is_override === right.is_override &&
    left.is_read_only === right.is_read_only &&
    sameValue(left.normalized_value, right.normalized_value)
  )
}

export function replaceDraftFieldFromSettings(
  draft: SettingsDraft,
  settings: SettingsDto,
  key: RuntimeSettingKey,
): SettingsDraft {
  const next: SettingsDraft = {
    values: cloneValues(draft.values),
    overrides: new Set(draft.overrides),
    readOnly: new Set(draft.readOnly),
  }
  if (settings.read_only.includes(key)) next.readOnly.add(key)
  else next.readOnly.delete(key)
  if (settings.overrides.includes(key)) next.overrides.add(key)
  else next.overrides.delete(key)
  if (key === 'header_rules') {
    next.values.header_rules = cloneHeaderRules(settings.values.header_rules)
  } else if (key === 'inject_usage_options') {
    next.values.inject_usage_options = settings.values.inject_usage_options
  } else if (key === 'affinity_enabled') {
    next.values.affinity_enabled = settings.values.affinity_enabled
  } else if (key === 'models_dev_auto_sync_enabled') {
    next.values.models_dev_auto_sync_enabled = settings.values.models_dev_auto_sync_enabled
  } else {
    next.values[key] = settings.values[key]
  }
  return next
}

export function buildSettingsPatch(
  base: SettingsDto,
  draft: SettingsDraft,
  scope: SettingsScope,
): SettingsPatch {
  const patch: SettingsPatch = {}
  const baseOverrides = new Set(base.overrides)
  const readOnly = new Set(base.read_only)
  for (const key of settingsScopeKeys(scope)) {
    if (readOnly.has(key) || draft.readOnly.has(key)) continue
    const wasOwned = baseOverrides.has(key)
    const isOwned = draft.overrides.has(key)
    if (wasOwned && !isOwned) {
      ;(patch as Record<string, unknown>)[key] = null
      continue
    }
    if (!isOwned) continue
    const value = normalizedWireValue(draft.values, key)
    if (
      !wasOwned ||
      !sameValue(
        normalizedIdentityValue(draft.values, key),
        normalizedIdentityValue(base.values, key),
      )
    ) {
      ;(patch as Record<string, unknown>)[key] = value
    }
  }
  return patch
}

export function rebaseSettingsDraft(
  base: SettingsDto,
  draft: SettingsDraft,
  refreshed: SettingsDto,
  scope: SettingsScope,
): SettingsDraft {
  const patch = buildSettingsPatch(base, draft, scope)
  const rebased = createSettingsDraft(refreshed)

  for (const key of settingsScopeKeys(scope)) {
    if (!Object.prototype.hasOwnProperty.call(patch, key)) continue
    if (rebased.readOnly.has(key)) continue
    const value = patch[key]
    if (value === null) {
      rebased.overrides.delete(key)
      continue
    }
    rebased.overrides.add(key)
    if (key === 'header_rules') {
      rebased.values.header_rules = cloneHeaderRules(value as HeaderRulesDto)
    } else if (key === 'inject_usage_options') {
      rebased.values.inject_usage_options = value as boolean
    } else if (key === 'affinity_enabled') {
      rebased.values.affinity_enabled = value as boolean
    } else if (key === 'models_dev_auto_sync_enabled') {
      rebased.values.models_dev_auto_sync_enabled = value as boolean
    } else {
      rebased.values[key] = value as number
    }
  }

  return rebased
}

export function isValidTimeout(value: number): boolean {
  return Number.isSafeInteger(value) && value > 0 && value <= 9_223_372_036
}

export function isValidRetention(value: number): boolean {
  return Number.isSafeInteger(value) && value >= 1 && value <= 365
}

export function isValidAffinityCapacity(value: number): boolean {
  return Number.isSafeInteger(value) && value >= 1 && value <= 1_000_000
}

export function isValidNonNegativeInteger(value: number): boolean {
  return Number.isSafeInteger(value) && value >= 0
}

function asciiLower(value: string): string {
  return value.replace(/[A-Z]/g, (character) => String.fromCharCode(character.charCodeAt(0) + 32))
}

export function validateSettingsSection(draft: SettingsDraft, section: SettingsSection): boolean {
  if (section === 'model-prices') return true
  if (section === 'affinity') {
    return (
      (!draft.overrides.has('affinity_ttl') || isValidTimeout(draft.values.affinity_ttl)) &&
      (!draft.overrides.has('affinity_capacity') ||
        isValidAffinityCapacity(draft.values.affinity_capacity))
    )
  }
  if (section === 'logs-maintenance') {
    return (
      !draft.overrides.has('request_log_retention_days') ||
      isValidRetention(draft.values.request_log_retention_days)
    )
  }
  const timeouts: TimeoutSettingKey[] = [
    'first_byte_timeout',
    'request_timeout',
    'stream_idle_timeout',
    'validation_interval',
  ]
  const policyCounts: PolicyCountSettingKey[] = ['retry_count', 'blacklist_threshold']
  return (
    timeouts.every((key) => !draft.overrides.has(key) || isValidTimeout(draft.values[key])) &&
    policyCounts.every(
      (key) => !draft.overrides.has(key) || isValidNonNegativeInteger(draft.values[key]),
    )
  )
}
