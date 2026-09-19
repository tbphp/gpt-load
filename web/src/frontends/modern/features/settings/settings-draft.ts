import {
  settingKeys,
  settingNumbers,
  type CORSConfig,
  type SettingKey,
  type SettingNumber,
  type SettingSwitch,
  type SettingsData,
  type SettingsPatch,
  type RouteStrategy,
} from '@modern/api/settings'
import type { HeaderRules } from '@modern/api/group-detail'
import { validProxyURL } from '@modern/app/proxy'
import {
  autoModelDraft,
  autoModelValue,
  defaultAutoModel,
  validAutoDraft,
  type AutoModelDraft,
} from '@modern/api/auto-model'

export type HeaderSetting = 'header_rules' | 'response_header_rules'
export interface HeaderRow {
  id: number
  action: 'set' | 'remove'
  name: string
  value: string
}
export interface CORSDraft {
  enabled: boolean
  allowed_origins: string
  allowed_methods: string
  allowed_headers: string
  exposed_headers: string
  allow_credentials: boolean
  max_age: string
}
export type SettingsDraft = Record<SettingNumber, string> &
  Record<SettingSwitch, boolean> & {
    route_strategy: RouteStrategy
    header_rules: HeaderRow[]
    response_header_rules: HeaderRow[]
    cors: CORSDraft
    proxy_config: { mode: 'inherit' | 'direct' | 'custom'; url: string }
    auto_model: AutoModelDraft
  }
let nextHeader = 0
export function newHeader(): HeaderRow {
  return { id: nextHeader++, action: 'set', name: '', value: '' }
}
function headerRows(value: HeaderRules): HeaderRow[] {
  return [
    ...Object.entries(value.set).map(([name, value]): HeaderRow => ({
      id: nextHeader++,
      action: 'set',
      name,
      value,
    })),
    ...value.remove.map((name): HeaderRow => ({
      id: nextHeader++,
      action: 'remove',
      name,
      value: '',
    })),
  ]
}
export function createSettingsDraft(data: SettingsData): SettingsDraft {
  const values = data.values
  return {
    ...values,
    ...(Object.fromEntries(
      Object.keys(settingNumbers).map((key) => [key, String(values[key as SettingNumber])]),
    ) as Record<SettingNumber, string>),
    header_rules: headerRows(values.header_rules),
    response_header_rules: headerRows(values.response_header_rules),
    proxy_config: { mode: values.proxy_config.configured_mode, url: '' },
    auto_model: autoModelDraft(values.auto_model ?? defaultAutoModel()),
    cors: {
      ...values.cors,
      allowed_origins: values.cors.allowed_origins.join('\n'),
      allowed_methods: values.cors.allowed_methods.join(', '),
      allowed_headers: values.cors.allowed_headers.join(', '),
      exposed_headers: values.cors.exposed_headers.join(', '),
      max_age: String(values.cors.max_age),
    },
  }
}
export function cloneDraft<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T
}
export function changedSettings(
  base: SettingsDraft,
  draft: SettingsDraft,
  resets: ReadonlySet<SettingKey>,
): SettingKey[] {
  return settingKeys.filter(
    (key) => resets.has(key) || JSON.stringify(base[key]) !== JSON.stringify(draft[key]),
  )
}
export function splitSettingList(value: string): string[] {
  return value
    .split(/[,\r\n]/u)
    .map((value) => value.trim())
    .filter(Boolean)
}
function normalizedOrigin(value: string): string {
  if (value === '*' || value === 'null') return value
  try {
    if (!/^[A-Za-z][A-Za-z0-9+.-]*:\/\/[^/?#\s,@]+$/u.test(value)) return value
    const url = new URL(value)
    return ['http:', 'https:'].includes(url.protocol)
      ? url.origin
      : url.protocol.toLowerCase() + '//' + url.host.toLowerCase()
  } catch {
    return value
  }
}
function corsValue(draft: CORSDraft): CORSConfig {
  return {
    enabled: draft.enabled,
    allow_credentials: draft.allow_credentials,
    max_age: Number(draft.max_age),
    allowed_origins: splitSettingList(draft.allowed_origins).map(normalizedOrigin),
    allowed_methods: splitSettingList(draft.allowed_methods).map((value) => value.toUpperCase()),
    allowed_headers: splitSettingList(draft.allowed_headers),
    exposed_headers: splitSettingList(draft.exposed_headers),
  }
}
function validInteger(value: string, min: number, max = Number.MAX_SAFE_INTEGER): boolean {
  return (
    /^\d+$/u.test(value.trim()) &&
    Number.isSafeInteger(Number(value)) &&
    Number(value) >= min &&
    Number(value) <= max
  )
}
function validToken(value: string): boolean {
  return (
    value.length > 0 &&
    [...value].every(
      (character) =>
        /^[A-Za-z0-9]$/u.test(character) ||
        "!#$%&'*+-.^_|~".includes(character) ||
        character.charCodeAt(0) === 96,
    )
  )
}
const connectionHeaders = [
  'connection',
  'proxy-connection',
  'keep-alive',
  'te',
  'trailer',
  'transfer-encoding',
  'upgrade',
]
const credentials = [
  'authorization',
  'proxy-authorization',
  'api-key',
  'x-api-key',
  'x-goog-api-key',
]
const requestForbidden = new Set([
  ...connectionHeaders,
  ...credentials,
  'cookie',
  'cookie2',
  'accept-encoding',
  'content-encoding',
])
const responseForbidden = new Set([
  ...connectionHeaders,
  ...credentials,
  'content-encoding',
  'content-length',
  'content-range',
  'content-type',
  'date',
  'server',
  'set-cookie',
  'set-cookie2',
  'vary',
  'access-control-allow-origin',
  'access-control-allow-methods',
  'access-control-allow-headers',
  'access-control-allow-credentials',
  'access-control-expose-headers',
  'access-control-max-age',
])
export function headerErrors(rows: HeaderRow[], key: HeaderSetting): Record<string, string> {
  const errors: Record<string, string> = {}
  const names = rows.map((row) => row.name.trim().toLowerCase())
  rows.forEach((row, index) => {
    const name = names[index]!
    const field = key + '.' + row.id
    if (!validToken(name)) errors[field + '.name'] = 'headerName'
    else if (names.filter((value) => value === name).length > 1)
      errors[field + '.name'] = 'headerDuplicate'
    else if (
      name.startsWith('proxy-') ||
      (key === 'response_header_rules'
        ? responseForbidden.has(name) || name.startsWith('x-gptload-')
        : requestForbidden.has(name))
    ) {
      errors[field + '.name'] = 'headerProtected'
    }
    if (row.action === 'set' && /[\u0000-\u0008\u000a-\u001f\u007f]/u.test(row.value))
      errors[field + '.value'] = 'headerValue'
  })
  return errors
}
function validUnique(
  values: string[],
  validate: (value: string) => boolean,
  ignoreCase = true,
): boolean {
  return (
    values.every(validate) &&
    new Set(values.map((value) => (ignoreCase ? value.toLowerCase() : value))).size ===
      values.length
  )
}
function validHeaders(values: string[]): boolean {
  return (
    validUnique(values, (value) => value === '*' || validToken(value)) &&
    (!values.includes('*') || values.length === 1)
  )
}
function validOrigin(value: string): boolean {
  if (value === '*' || value === 'null') return true
  if (!/^[A-Za-z][A-Za-z0-9+.-]*:\/\/[^/?#\s,@]+$/u.test(value)) return false
  try {
    const url = new URL(value)
    return Boolean(url.hostname) && !url.username && !url.password
  } catch {
    return false
  }
}
export function settingsErrors(
  base: SettingsData,
  draft: SettingsDraft,
  changed: readonly SettingKey[],
  resets: ReadonlySet<SettingKey>,
): Record<string, string> {
  const errors: Record<string, string> = {}
  for (const key of changed) {
    if (resets.has(key) || base.readOnly.includes(key)) continue
    if (key === 'auto_model' && !validAutoDraft(draft.auto_model)) errors.auto_model = 'autoModel'
    if (key in settingNumbers) {
      const number = key as SettingNumber
      const rule = settingNumbers[number]
      if (!validInteger(draft[number], rule.min, rule.max)) errors[key] = 'number'
    } else if (key === 'header_rules' || key === 'response_header_rules') {
      Object.assign(errors, headerErrors(draft[key], key))
    } else if (key === 'proxy_config') {
      const proxy = draft.proxy_config
      const unchangedURL =
        proxy.mode === base.values.proxy_config.configured_mode &&
        (!proxy.url.trim() || proxy.url.trim() === base.values.proxy_config.display_url)
      if (proxy.mode === 'custom' && !unchangedURL && !validProxyURL(proxy.url.trim()))
        errors.proxy_config = 'proxy'
    } else if (key === 'cors') {
      const cors = corsValue(draft.cors)
      if (
        !validUnique(cors.allowed_origins, validOrigin, false) ||
        (cors.enabled && !cors.allowed_origins.length) ||
        (cors.allowed_origins.includes('*') &&
          (cors.allowed_origins.length > 1 || cors.allow_credentials))
      )
        errors['cors.allowed_origins'] = 'origins'
      if (
        !validUnique(cors.allowed_methods, (value) => value !== '*' && validToken(value)) ||
        (cors.enabled && !cors.allowed_methods.length)
      )
        errors['cors.allowed_methods'] = 'methods'
      if (!validHeaders(cors.allowed_headers) || (cors.enabled && !cors.allowed_headers.length))
        errors['cors.allowed_headers'] = 'headers'
      if (
        !validHeaders(cors.exposed_headers) ||
        (cors.allow_credentials && cors.exposed_headers.includes('*'))
      )
        errors['cors.exposed_headers'] = 'headers'
      if (!validInteger(draft.cors.max_age, 0)) errors['cors.max_age'] = 'maxAge'
    }
  }
  return errors
}
export function buildSettingsPatch(
  base: SettingsData,
  draft: SettingsDraft,
  changed: readonly SettingKey[],
  resets: ReadonlySet<SettingKey>,
): SettingsPatch {
  const patch: Record<string, unknown> = {}
  for (const key of changed) {
    if (base.readOnly.includes(key)) continue
    if (resets.has(key)) {
      patch[key] = null
      continue
    }
    if (key in settingNumbers) patch[key] = Number(draft[key as SettingNumber])
    else if (key === 'header_rules' || key === 'response_header_rules') {
      patch[key] = {
        set: Object.fromEntries(
          draft[key]
            .filter((row) => row.action === 'set')
            .map((row) => [row.name.trim(), row.value]),
        ),
        remove: draft[key].filter((row) => row.action === 'remove').map((row) => row.name.trim()),
      }
    } else if (key === 'auto_model') patch[key] = autoModelValue(draft.auto_model)
    else if (key === 'cors') patch[key] = corsValue(draft.cors)
    else if (key === 'proxy_config') {
      const proxy = draft.proxy_config
      if (
        proxy.mode === base.values.proxy_config.configured_mode &&
        (proxy.mode !== 'custom' ||
          !proxy.url.trim() ||
          proxy.url.trim() === base.values.proxy_config.display_url)
      )
        continue
      patch[key] =
        proxy.mode === 'inherit'
          ? null
          : proxy.mode === 'direct'
            ? { mode: 'direct' }
            : { mode: 'custom', url: proxy.url.trim() }
    } else patch[key] = draft[key]
  }
  return patch as SettingsPatch
}
