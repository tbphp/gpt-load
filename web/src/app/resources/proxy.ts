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

export interface ProxyDraftState {
  dirty: boolean
  invalid: boolean
  value: ProxyMutation | undefined
}

/**
 * 已存自定义地址的 placeholder；没有可展示的地址时返回 undefined，由调用方回退到通用提示。
 * 输入框不做回填：后端 Display() 会把密码脱敏成 ******，回填等于把掩码当真密码存回去。
 */
export function proxyPlaceholderURL(view: ProxyViewDto): string | undefined {
  return view.configured_mode === 'custom' ? view.display_url : undefined
}

/**
 * 覆盖开关要切到的模式。重新开启覆盖时回到基线已存的覆盖模式，让「恢复默认 / 继承」
 * 可以原路撤销而不丢掉原本配置；基线本就未覆盖时落到 direct——一个立即完整有效的状态。
 */
export function proxyOverrideToggleMode(
  base: ProxyViewDto,
  overridden: boolean,
): ProxyConfiguredMode {
  if (overridden) return 'inherit'
  return base.configured_mode === 'inherit' ? 'direct' : base.configured_mode
}

/**
 * 同模式下有两种“未改动”：输入留空表示保持原地址；输入与已存地址逐字相同同样不是改动
 * ——后者顺带挡住了照着 placeholder 手敲掩码提交的情况。
 */
export function proxyDraftState(
  base: ProxyViewDto,
  mode: ProxyConfiguredMode,
  endpoint: string,
): ProxyDraftState {
  const trimmed = endpoint.trim()
  const unchanged =
    mode === base.configured_mode &&
    (mode !== 'custom' || trimmed === '' || trimmed === (base.display_url ?? ''))
  if (unchanged) return { dirty: false, invalid: false, value: undefined }
  const value = proxyMutation(mode, endpoint)
  return { dirty: true, invalid: value === undefined, value }
}

export function isValidProxyURL(value: string): boolean {
  if (value === '' || value !== value.trim() || value.includes('?') || value.includes('#')) {
    return false
  }

  const lowerValue = value.toLowerCase()
  if (!lowerValue.startsWith('http://') && !lowerValue.startsWith('socks5://')) return false

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
  if (
    host.endsWith(':') ||
    parsed.port === '0' ||
    (parsed.protocol === 'socks5:' && parsed.port === '')
  ) {
    return false
  }
  if (parsed.pathname !== '' && parsed.pathname !== '/') return false
  if (authority.includes('@') && (parsed.username === '' || parsed.password === '')) return false
  return true
}
