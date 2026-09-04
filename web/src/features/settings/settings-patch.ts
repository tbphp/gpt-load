import type { HeaderRulesDto } from '@/app/resources/groups'
import type {
  RuntimeSettingKey,
  PolicyCountSettingKey,
  CORSConfigDto,
  SettingsDto,
  SettingsPatch,
  SettingsValues,
  TimeoutSettingKey,
} from '@/app/resources/settings'
import { runtimeSettingKeys } from '@/app/resources/settings'

export type SettingsSection =
  'request-forwarding' | 'affinity' | 'browser-access' | 'logs-maintenance' | 'model-prices'
export type SettingsScope = SettingsSection | 'all'

export interface SettingsDraft {
  values: SettingsValues
  overrides: Set<RuntimeSettingKey>
  readOnly: Set<RuntimeSettingKey>
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
const browserAccessKeys: RuntimeSettingKey[] = ['cors', 'response_header_rules']
const modelPriceKeys: RuntimeSettingKey[] = ['models_dev_auto_sync_enabled']

function cloneHeaderRules(value: HeaderRulesDto): HeaderRulesDto {
  return { set: { ...value.set }, remove: [...value.remove] }
}

function cloneCORSConfig(value: CORSConfigDto): CORSConfigDto {
  return {
    ...value,
    allowed_origins: [...value.allowed_origins],
    allowed_methods: [...value.allowed_methods],
    allowed_headers: [...value.allowed_headers],
    exposed_headers: [...value.exposed_headers],
  }
}

function cloneValues(value: SettingsValues): SettingsValues {
  return {
    ...value,
    header_rules: cloneHeaderRules(value.header_rules),
    cors: cloneCORSConfig(value.cors),
    response_header_rules: cloneHeaderRules(value.response_header_rules),
  }
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
    } else if (key === 'cors') {
      next.values.cors = cloneCORSConfig(base.values.cors)
    } else if (key === 'response_header_rules') {
      next.values.response_header_rules = cloneHeaderRules(base.values.response_header_rules)
    } else if (key !== 'header_rules') {
      next.values[key] = base.values[key]
    }
  } else {
    next.overrides.delete(key)
    if (key === 'header_rules') next.values.header_rules = { set: {}, remove: [] }
    if (key === 'response_header_rules') next.values.response_header_rules = { set: {}, remove: [] }
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
): number | boolean | HeaderRulesDto | CORSConfigDto {
  if (key === 'header_rules' || key === 'response_header_rules')
    return normalizeHeaderRules(settings[key])
  if (key === 'cors') return normalizeCORSConfig(settings.cors)
  return settings[key]
}

function normalizeCORSConfig(value: CORSConfigDto): CORSConfigDto {
  return {
    ...cloneCORSConfig(value),
    allowed_origins: value.allowed_origins.map((entry) => entry.trim()),
    allowed_methods: value.allowed_methods.map((entry) => entry.trim().toUpperCase()),
    allowed_headers: value.allowed_headers.map((entry) => entry.trim()),
    exposed_headers: value.exposed_headers.map((entry) => entry.trim()),
  }
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
): number | boolean | HeaderRulesDto | CORSConfigDto {
  if (key === 'header_rules' || key === 'response_header_rules')
    return canonicalHeaderRulesIdentity(settings[key])
  if (key === 'cors') return canonicalCORSIdentity(settings.cors)
  return normalizedWireValue(settings, key)
}

function canonicalCORSIdentity(value: CORSConfigDto): CORSConfigDto {
  const normalized = normalizeCORSConfig(value)
  return {
    ...normalized,
    allowed_origins: [...normalized.allowed_origins].sort(),
    allowed_methods: [...normalized.allowed_methods].sort(),
    allowed_headers: normalized.allowed_headers.map(asciiLower).sort(),
    exposed_headers: normalized.exposed_headers.map(asciiLower).sort(),
  }
}

function sameValue(left: unknown, right: unknown): boolean {
  return JSON.stringify(left) === JSON.stringify(right)
}

export function settingsSectionKeys(section: SettingsSection): RuntimeSettingKey[] {
  if (section === 'request-forwarding') return [...requestForwardingKeys]
  if (section === 'affinity') return [...affinityKeys]
  if (section === 'browser-access') return [...browserAccessKeys]
  if (section === 'logs-maintenance') return [...logsMaintenanceKeys]
  return [...modelPriceKeys]
}

export function settingsScopeKeys(scope: SettingsScope): RuntimeSettingKey[] {
  return scope === 'all' ? [...runtimeSettingKeys] : settingsSectionKeys(scope)
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

export function isValidCORSConfig(value: CORSConfigDto): boolean {
  if (!isValidNonNegativeInteger(value.max_age)) return false
  if (!validUniqueList(value.allowed_origins, false, (entry) => validOrigin(entry))) return false
  if (value.allowed_origins.includes('*') && value.allowed_origins.length > 1) return false
  if (!validUniqueList(value.allowed_methods, true, (entry) => entry !== '*' && isHTTPToken(entry)))
    return false
  if (!validHeaderList(value.allowed_headers, value.enabled)) return false
  if (!validHeaderList(value.exposed_headers, false)) return false
  if (value.enabled && (value.allowed_origins.length === 0 || value.allowed_methods.length === 0))
    return false
  if (value.enabled && value.allowed_headers.length === 0) return false
  if (value.allow_credentials && value.allowed_origins.includes('*')) return false
  if (value.allow_credentials && value.exposed_headers.includes('*')) return false
  return true
}

function validOrigin(value: string): boolean {
  if (value === '*' || value === 'null') return true
  return (
    value === value.trim() &&
    !value.includes('@') &&
    /^[A-Za-z][A-Za-z0-9+.-]*:\/\/[^/?#\s,]+$/u.test(value)
  )
}

function validHeaderList(values: string[], required: boolean): boolean {
  if (required && values.length === 0) return false
  if (!validUniqueList(values, true, (entry) => entry === '*' || isHTTPToken(entry))) return false
  return !values.includes('*') || values.length === 1
}

function validUniqueList(
  values: string[],
  caseInsensitive: boolean,
  validate: (value: string) => boolean,
): boolean {
  const seen = new Set<string>()
  for (const value of values) {
    if (!validate(value)) return false
    const identity = caseInsensitive ? asciiLower(value) : value
    if (seen.has(identity)) return false
    seen.add(identity)
  }
  return true
}

function isHTTPToken(value: string): boolean {
  return value.length > 0 && /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/u.test(value)
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
  if (section === 'browser-access') {
    return !draft.overrides.has('cors') || isValidCORSConfig(draft.values.cors)
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
