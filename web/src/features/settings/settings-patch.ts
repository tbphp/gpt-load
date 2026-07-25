import type { HeaderRulesDto } from '@/api/control/groups'
import type {
  RuntimeSettingKey,
  SettingsDto,
  SettingsPatch,
  SettingsValues,
  TimeoutSettingKey,
} from '@/api/control/settings'

export type SettingsSection = 'request-forwarding' | 'logs-maintenance'

export interface SettingsDraft {
  values: SettingsValues
  overrides: Set<RuntimeSettingKey>
}

const requestForwardingKeys: RuntimeSettingKey[] = [
  'connect_timeout',
  'first_byte_timeout',
  'request_timeout',
  'stream_idle_timeout',
  'header_rules',
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
    if (key === 'header_rules')
      next.values.header_rules = cloneHeaderRules(base.values.header_rules)
    else next.values[key] = base.values[key]
  } else {
    next.overrides.delete(key)
  }
  return next
}

function normalizeHeaderRules(value: HeaderRulesDto): HeaderRulesDto {
  const set = Object.fromEntries(
    Object.entries(value.set)
      .map(([name, headerValue]) => [name.trim(), headerValue] as const)
      .filter(([name]) => name.length > 0)
      .sort(([left], [right]) => left.localeCompare(right)),
  )
  const remove = [...new Set(value.remove.map((name) => name.trim()).filter(Boolean))].sort(
    (a, b) => a.localeCompare(b),
  )
  return { set, remove }
}

function normalizedValue(
  settings: SettingsValues,
  key: RuntimeSettingKey,
): number | HeaderRulesDto {
  return key === 'header_rules' ? normalizeHeaderRules(settings.header_rules) : settings[key]
}

function sameValue(left: unknown, right: unknown): boolean {
  return JSON.stringify(left) === JSON.stringify(right)
}

function sectionKeys(section: SettingsSection): RuntimeSettingKey[] {
  return section === 'request-forwarding' ? requestForwardingKeys : logsMaintenanceKeys
}

export function buildSettingsPatch(
  base: SettingsDto,
  draft: SettingsDraft,
  section: SettingsSection,
): SettingsPatch {
  const patch: SettingsPatch = {}
  const baseOverrides = new Set(base.overrides)
  for (const key of sectionKeys(section)) {
    const wasOwned = baseOverrides.has(key)
    const isOwned = draft.overrides.has(key)
    if (wasOwned && !isOwned) {
      ;(patch as Record<string, unknown>)[key] = null
      continue
    }
    if (!isOwned) continue
    const value = normalizedValue(draft.values, key)
    if (!wasOwned || !sameValue(value, normalizedValue(base.values, key))) {
      ;(patch as Record<string, unknown>)[key] = value
    }
  }
  return patch
}

export function isValidTimeout(value: number): boolean {
  return Number.isSafeInteger(value) && value > 0
}

export function isValidRetention(value: number): boolean {
  return Number.isSafeInteger(value) && value >= 1 && value <= 365
}

function asciiLower(value: string): string {
  return value.replace(/[A-Z]/g, (character) => String.fromCharCode(character.charCodeAt(0) + 32))
}

export function hasDuplicateHeaderNames(value: HeaderRulesDto): boolean {
  const names = [...Object.keys(value.set), ...value.remove]
    .map((name) => asciiLower(name.trim()))
    .filter(Boolean)
  return new Set(names).size !== names.length
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
  return (
    timeouts.every((key) => !draft.overrides.has(key) || isValidTimeout(draft.values[key])) &&
    (!draft.overrides.has('header_rules') || !hasDuplicateHeaderNames(draft.values.header_rules))
  )
}
