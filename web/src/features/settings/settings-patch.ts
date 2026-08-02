import type { HeaderRulesDto } from '@/app/resources/groups'
import type {
  RuntimeSettingKey,
  SettingsDto,
  SettingsPatch,
  SettingsValues,
  TimeoutSettingKey,
} from '@/app/resources/settings'
import { runtimeSettingKeys } from '@/app/resources/settings'

export type SettingsSection = 'request-forwarding' | 'logs-maintenance'
export type SettingsScope = SettingsSection | 'all'

export interface SettingsDraft {
  values: SettingsValues
  overrides: Set<RuntimeSettingKey>
}

export interface SettingsFieldIdentity {
  is_override: boolean
  normalized_value: number | boolean | HeaderRulesDto
}

const requestForwardingKeys: RuntimeSettingKey[] = [
  'connect_timeout',
  'first_byte_timeout',
  'request_timeout',
  'stream_idle_timeout',
  'header_rules',
  'inject_usage_options',
]
const logsMaintenanceKeys: RuntimeSettingKey[] = ['request_log_retention_days']

function cloneHeaderRules(value: HeaderRulesDto): HeaderRulesDto {
  return { set: { ...value.set }, remove: [...value.remove] }
}

function cloneValues(value: SettingsValues): SettingsValues {
  return { ...value, header_rules: cloneHeaderRules(value.header_rules) }
}

export function createSettingsDraft(settings: SettingsDto): SettingsDraft {
  return { values: cloneValues(settings.values), overrides: new Set(settings.overrides) }
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
  }
  if (enabled) {
    next.overrides.add(key)
    if (key === 'inject_usage_options') {
      next.values.inject_usage_options = base.values.inject_usage_options
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
  if (key === 'inject_usage_options') return settings.inject_usage_options
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
  return [...(section === 'request-forwarding' ? requestForwardingKeys : logsMaintenanceKeys)]
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
    normalized_value: normalizedIdentityValue(settings.values, key),
  }
}

export function draftFieldIdentity(
  draft: SettingsDraft,
  key: RuntimeSettingKey,
): SettingsFieldIdentity {
  return {
    is_override: draft.overrides.has(key),
    normalized_value: normalizedIdentityValue(draft.values, key),
  }
}

export function sameSettingsFieldIdentity(
  left: SettingsFieldIdentity,
  right: SettingsFieldIdentity,
): boolean {
  return (
    left.is_override === right.is_override &&
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
  }
  if (settings.overrides.includes(key)) next.overrides.add(key)
  else next.overrides.delete(key)
  if (key === 'header_rules') {
    next.values.header_rules = cloneHeaderRules(settings.values.header_rules)
  } else if (key === 'inject_usage_options') {
    next.values.inject_usage_options = settings.values.inject_usage_options
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
  for (const key of settingsScopeKeys(scope)) {
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

function asciiLower(value: string): string {
  return value.replace(/[A-Z]/g, (character) => String.fromCharCode(character.charCodeAt(0) + 32))
}

export function validateSettingsSection(draft: SettingsDraft, section: SettingsSection): boolean {
  if (section === 'logs-maintenance') {
    return (
      !draft.overrides.has('request_log_retention_days') ||
      isValidRetention(draft.values.request_log_retention_days)
    )
  }
  const timeouts: TimeoutSettingKey[] = [
    'connect_timeout',
    'first_byte_timeout',
    'request_timeout',
    'stream_idle_timeout',
  ]
  return timeouts.every((key) => !draft.overrides.has(key) || isValidTimeout(draft.values[key]))
}
