import { formatLocalDateTime, parseLocalDateTime } from '@modern/components/ui/date-time'
import type { AccessInput, AccessKey, AccessScope, CostRule } from '@modern/api/access-keys'
import { createOperationKey } from '../groups/group-create-operation'

export type ScopeDimension = 'groups' | 'protocols' | 'models'
export interface RuleDraft extends CostRule {
  clientKey: string
  period: string
  unit: string
}
export interface AccessDraft {
  name: string
  key: string
  enabled: boolean
  never: boolean
  expires: string
  rpm: string
  price: string
  scope: AccessScope
  modes: Record<ScopeDimension, string>
  source: string
  cidrs: string
  rules: RuleDraft[]
}
export function localDate(value: number | null): string {
  return value === null ? '' : formatLocalDateTime(value)
}
export const periodUnits = { hour: 3600, day: 86400 } as const
export function ruleDraft(rule: CostRule): RuleDraft {
  const seconds = rule.period_seconds ?? 18000
  const unit = seconds % 86400 === 0 ? 'day' : 'hour'
  return {
    ...rule,
    clientKey: rule.id === undefined ? createOperationKey() : 'rule-' + rule.id,
    period: String(seconds / periodUnits[unit]),
    unit,
  }
}
export function draftFor(row?: AccessKey, duplicate = false): AccessDraft {
  const scope = row
    ? {
        groups: [...row.filters.groups],
        protocols: [...row.filters.protocols],
        models: [...row.filters.models],
        allowed_cidrs: [...row.filters.allowed_cidrs],
      }
    : { groups: [], protocols: [], models: [], allowed_cidrs: [] }
  return {
    name: row?.name ?? '',
    key: '',
    enabled: row?.status !== 'disabled',
    never: row?.expires_at_ms == null,
    expires: localDate(row?.expires_at_ms ?? null),
    rpm: String(row?.rpm_limit ?? 0),
    price: row?.price_multiplier ?? '1',
    scope,
    modes: {
      groups: scope.groups.length ? 'specified' : 'all',
      protocols: scope.protocols.length ? 'specified' : 'all',
      models: scope.models.length ? 'specified' : 'all',
    },
    source: scope.allowed_cidrs.length ? 'specified' : 'all',
    cidrs: scope.allowed_cidrs.join('\n'),
    rules: (row?.cost_limit_rules ?? []).map((rule) =>
      ruleDraft({ ...rule, ...(duplicate ? { id: undefined } : {}) }),
    ),
  }
}
const unique = <T>(values: T[]) => [...new Set(values)]
const clean = (values: string[]) => unique(values.map((value) => value.trim()).filter(Boolean))
export function normalizeDecimal(value: string): string {
  if (!/^\d+(?:\.\d{1,9})?$/.test(value.trim())) return value.trim()
  const [whole = '', fraction = ''] = value.trim().split('.')
  const normalized = fraction.replace(/0+$/, '')
  const integer = whole.replace(/^0+/, '') || '0'
  return normalized ? `${integer}.${normalized}` : integer
}
function periodSeconds(rule: RuleDraft): number {
  const seconds = Number(rule.period) * periodUnits[rule.unit as keyof typeof periodUnits]
  // 小时小数换算可能产生浮点尾差，不改变已有规则的整秒时长。
  const rounded = Math.round(seconds)
  return Math.abs(seconds - rounded) < 0.0000001 ? rounded : seconds
}
export function inputFor(draft: AccessDraft): AccessInput {
  return {
    name: draft.name.trim(),
    ...(draft.key ? { key: draft.key } : {}),
    status: draft.enabled ? 'active' : 'disabled',
    expires_at_ms: draft.never ? null : (parseLocalDateTime(draft.expires)?.getTime() ?? NaN),
    rpm_limit: Number(draft.rpm || 0),
    price_multiplier: normalizeDecimal(draft.price),
    filters: {
      groups: draft.modes.groups === 'all' ? [] : unique(draft.scope.groups),
      protocols: draft.modes.protocols === 'all' ? [] : clean(draft.scope.protocols),
      models: draft.modes.models === 'all' ? [] : clean(draft.scope.models),
      allowed_cidrs: draft.source === 'all' ? [] : clean(draft.cidrs.split(/\r?\n/)),
    },
    cost_limit_rules: draft.rules.map((rule) => ({
      ...(rule.id !== undefined ? { id: rule.id } : {}),
      kind: rule.kind,
      limit_usd: normalizeDecimal(rule.limit_usd),
      ...(rule.kind === 'periodic' ? { period_seconds: periodSeconds(rule) } : {}),
    })),
  }
}
function scopeJSON(scope: AccessScope): string {
  return JSON.stringify(
    Object.fromEntries(Object.entries(scope).map(([key, values]) => [key, [...values].sort()])),
  )
}
const ruleJSON = (rules: CostRule[]) =>
  JSON.stringify(
    rules
      .map((rule) => ({
        id: rule.id,
        kind: rule.kind,
        limit_usd: normalizeDecimal(rule.limit_usd),
        period_seconds: rule.kind === 'periodic' ? rule.period_seconds : 0,
      }))
      .sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b))),
  )
export function patchFor(base: AccessKey, input: AccessInput): Partial<AccessInput> {
  const patch: Partial<AccessInput> = {}
  if (input.key) patch.key = input.key
  if (base.name !== input.name) patch.name = input.name
  if (base.status !== input.status) patch.status = input.status
  if (base.expires_at_ms !== input.expires_at_ms) patch.expires_at_ms = input.expires_at_ms
  if (base.rpm_limit !== input.rpm_limit) patch.rpm_limit = input.rpm_limit
  if (normalizeDecimal(base.price_multiplier) !== input.price_multiplier)
    patch.price_multiplier = input.price_multiplier
  if (scopeJSON(base.filters) !== scopeJSON(input.filters)) patch.filters = input.filters
  if (ruleJSON(base.cost_limit_rules) !== ruleJSON(input.cost_limit_rules))
    patch.cost_limit_rules = input.cost_limit_rules
  return patch
}
export function draftErrors(draft: AccessDraft, base?: AccessKey): Record<string, string> {
  const errors: Record<string, string> = {}
  const input = inputFor(draft)
  if (
    !input.name ||
    new TextEncoder().encode(input.name).length > 255 ||
    /\p{Cc}/u.test(input.name)
  )
    errors.name = 'required'
  if (draft.key && !/^[\x21-\x7e]{1,256}$/.test(draft.key)) errors.key = 'invalidKey'
  if (!/^\d*$/.test(draft.rpm) || !Number.isSafeInteger(input.rpm_limit))
    errors.rpm = 'invalidNumber'
  if (
    !/^(?:0|[1-9]\d{0,3})(?:\.\d{1,6})?$/.test(input.price_multiplier) ||
    Number(input.price_multiplier) > 1000
  )
    errors.price = 'invalidNumber'
  if (
    !draft.never &&
    (!Number.isSafeInteger(input.expires_at_ms) ||
      (input.expires_at_ms !== base?.expires_at_ms && input.expires_at_ms! <= Date.now()))
  )
    errors.expires = 'invalidExpiry'
  for (const key of ['groups', 'protocols', 'models'] as const)
    if (draft.modes[key] !== 'all' && !input.filters[key].length) errors[key] = 'scopeRequired'
  if (input.filters.models.some((model) => /\p{Cc}/u.test(model))) errors.models = 'scopeRequired'
  if (
    draft.source !== 'all' &&
    (!input.filters.allowed_cidrs.length || input.filters.allowed_cidrs.length > 64)
  )
    errors.cidrs = 'invalidCIDR'
  const periods = new Set<number>()
  let totals = 0
  let periodic = 0
  for (const rule of input.cost_limit_rules) {
    if (!/^(0|[1-9]\d*)(\.\d{1,9})?$/.test(rule.limit_usd) || /^0(?:\.0+)?$/.test(rule.limit_usd))
      errors.rules = 'invalidQuota'
    if (rule.kind === 'total') totals++
    else {
      periodic++
      const seconds = rule.period_seconds!
      if (
        !Number.isSafeInteger(seconds) ||
        seconds < 60 ||
        seconds > 31536000 ||
        periods.has(seconds)
      )
        errors.rules = 'invalidQuota'
      periods.add(seconds)
    }
  }
  if (totals > 1 || periodic > 10) errors.rules = 'invalidQuota'
  return errors
}
export function keyStrength(value: string): 'weak' | 'fair' | 'strong' | undefined {
  if (!value) return undefined
  const content = value.startsWith('sk-gl-') ? value.slice(6) : value
  if (
    content.length < 12 ||
    new Set(content).size < 4 ||
    /^(.{1,4})\1+$/.test(content) ||
    ['012345678901234567890', 'abcdefghijklmnopqrstuvwxyz', 'qwertyuiopasdfghjklzxcvbnm'].some(
      (sequence) => sequence.includes(content.toLowerCase()),
    )
  )
    return 'weak'
  return content.length >= 20 ||
    (content.length >= 16 &&
      [/[a-z]/, /[A-Z]/, /\d/, /[^a-zA-Z0-9]/].filter((pattern) => pattern.test(content)).length >=
        3)
    ? 'strong'
    : 'fair'
}
export function generateKey(): string {
  const bytes = new Uint8Array(32)
  globalThis.crypto.getRandomValues(bytes)
  return 'sk-gl-' + Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('')
}
