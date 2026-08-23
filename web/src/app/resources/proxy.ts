import type {
  ProxyConfiguredMode,
  ProxyEffectiveMode,
  ProxyEffectiveSource,
  ProxyMutation,
  ProxyViewDto,
} from '@/api/control/types'
import { InvalidResponseError } from '@/api/errors'

import {
  assertNoSecretLikeFields,
  projectBoolean,
  projectEnum,
  projectRecord,
  projectString,
} from './projector'

const configuredModes = ['inherit', 'direct', 'custom'] as const
const effectiveModes = ['direct', 'environment', 'custom'] as const
const effectiveSources = ['credential', 'group', 'global', 'environment', 'default'] as const
const proxyViewFields = [
  'configured_mode',
  'effective_mode',
  'effective_source',
  'display_url',
  'has_auth',
] as const

export function projectProxyView(value: unknown): ProxyViewDto {
  const record = projectRecord(value)
  assertNoSecretLikeFields(record, proxyViewFields)
  const configuredMode = projectEnum(record.configured_mode, configuredModes)
  const effectiveMode = projectEnum(record.effective_mode, effectiveModes)
  const effectiveSource = projectEnum(record.effective_source, effectiveSources)
  const displayURL =
    record.display_url === undefined ? undefined : projectString(record.display_url)
  const hasAuth = projectBoolean(record.has_auth)

  if ((effectiveMode === 'custom') !== (displayURL !== undefined) || (hasAuth && !displayURL)) {
    throw new InvalidResponseError()
  }

  return {
    configured_mode: configuredMode as ProxyConfiguredMode,
    effective_mode: effectiveMode as ProxyEffectiveMode,
    effective_source: effectiveSource as ProxyEffectiveSource,
    ...(displayURL === undefined ? {} : { display_url: displayURL }),
    has_auth: hasAuth,
  }
}

export function proxyMutation(
  mode: ProxyConfiguredMode,
  endpoint: string,
): ProxyMutation | undefined {
  if (mode === 'inherit') return null
  if (mode === 'direct') return { mode: 'direct' }

  const normalized = endpoint.trim()
  if (!isValidProxyURL(normalized)) return undefined
  return { mode: 'custom', url: normalized }
}

export function isValidProxyURL(value: string): boolean {
  if (value === '' || value !== value.trim() || value.includes('?') || value.includes('#')) {
    return false
  }

  let parsed: URL
  try {
    parsed = new URL(value)
  } catch {
    return false
  }

  if (!['http:', 'socks5:'].includes(parsed.protocol) || parsed.hostname === '') {
    return false
  }
  const authorityStart = value.indexOf('://') + 3
  const pathStart = value.indexOf('/', authorityStart)
  const authority = value.slice(authorityStart, pathStart === -1 ? undefined : pathStart)
  const host = authority.slice(authority.lastIndexOf('@') + 1)
  if (host.endsWith(':') || (parsed.protocol === 'socks5:' && parsed.port === '')) return false
  if (parsed.pathname !== '' && parsed.pathname !== '/') return false
  return parsed.password === '' || parsed.username !== ''
}
