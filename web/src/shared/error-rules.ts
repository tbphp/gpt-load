import { InvalidResponseError } from '@shared/http/errors'

export const errorRuleRetries = ['none', 'next_candidate'] as const
export const errorRuleEffects = [
  'none',
  'cooldown_model',
  'cooldown_credential',
  'record_credential_failure',
  'skip_group',
] as const
export const maxErrorRuleCooldown = 9_223_372_036
const maxErrorRulesBytes = 65_535
export interface ErrorRule {
  status_codes?: number[]
  keywords?: string[]
  retry: (typeof errorRuleRetries)[number]
  effect: (typeof errorRuleEffects)[number]
  cooldown_seconds?: number
}
export interface ErrorRuleDraft {
  key: number
  open: boolean
  statuses: string
  statusInput: string
  statusInvalid: boolean
  keywords: string
  keywordInput: string
  retry: ErrorRule['retry']
  effect: ErrorRule['effect']
  cooldown: string
}
export function cloneErrorRules(rules: ErrorRule[]): ErrorRule[] {
  return rules.map((rule) => ({
    ...rule,
    ...(rule.status_codes ? { status_codes: [...rule.status_codes] } : {}),
    ...(rule.keywords ? { keywords: [...rule.keywords] } : {}),
  }))
}
export function hasErrorRuleCooldown(effect: ErrorRule['effect']): boolean {
  return effect === 'cooldown_model' || effect === 'cooldown_credential'
}
export function validErrorRules(rules: ErrorRule[]): boolean {
  return (
    rules.length <= 100 &&
    rules.every((rule) => {
      const statuses = rule.status_codes ?? []
      const keywords = rule.keywords ?? []
      return (
        (statuses.length > 0 ||
          keywords.some((value) => typeof value === 'string' && value.trim().length > 0)) &&
        statuses.every((code) => Number.isSafeInteger(code) && code >= 200 && code <= 599) &&
        keywords.every((word) => typeof word === 'string' && word.trim().length > 0) &&
        errorRuleRetries.includes(rule.retry) &&
        errorRuleEffects.includes(rule.effect) &&
        (hasErrorRuleCooldown(rule.effect)
          ? Number.isSafeInteger(rule.cooldown_seconds) &&
            Number(rule.cooldown_seconds) > 0 &&
            Number(rule.cooldown_seconds) <= maxErrorRuleCooldown
          : rule.cooldown_seconds === undefined)
      )
    }) &&
    // 按后端 json.Marshal 的转义方式计算，避免浏览器低估实际存储字节数。
    new TextEncoder().encode(
      JSON.stringify(rules).replace(
        /[<>&\u2028\u2029]/gu,
        (value) => `\\u${value.charCodeAt(0).toString(16).padStart(4, '0')}`,
      ),
    ).length <= maxErrorRulesBytes
  )
}
export function readErrorRules(value: unknown): ErrorRule[] {
  if (!Array.isArray(value)) throw new InvalidResponseError()
  const fields = ['status_codes', 'keywords', 'retry', 'effect', 'cooldown_seconds']
  const rules = value.map((item): ErrorRule => {
    if (!item || typeof item !== 'object' || Array.isArray(item)) throw new InvalidResponseError()
    const row = item as Record<string, unknown>
    if (
      Object.keys(row).some((key) => !fields.includes(key)) ||
      (row.status_codes != null && !Array.isArray(row.status_codes)) ||
      (row.keywords != null && !Array.isArray(row.keywords))
    )
      throw new InvalidResponseError()
    const statuses = (row.status_codes ?? []) as number[]
    const keywords = (row.keywords ?? []) as unknown[]
    if (keywords.some((word) => typeof word !== 'string')) throw new InvalidResponseError()
    const seen = new Set<string>()
    const normalizedKeywords = (keywords as string[])
      .map((word) => word.trim())
      .filter((word) => {
        const key = word.toLowerCase()
        if (!word || seen.has(key)) return false
        seen.add(key)
        return true
      })
    const rule: ErrorRule = {
      ...(statuses.length ? { status_codes: [...new Set(statuses)] } : {}),
      ...(normalizedKeywords.length ? { keywords: normalizedKeywords } : {}),
      retry: row.retry as ErrorRule['retry'],
      effect: row.effect as ErrorRule['effect'],
      ...(row.cooldown_seconds != null && row.cooldown_seconds !== 0
        ? { cooldown_seconds: row.cooldown_seconds as number }
        : {}),
    }
    if (!validErrorRules([rule])) throw new InvalidResponseError()
    return cloneErrorRules([rule])[0]!
  })
  if (!validErrorRules(rules)) throw new InvalidResponseError()
  return rules
}
export function errorRuleDraft(rule: ErrorRule, key: number, open = false): ErrorRuleDraft {
  return {
    key,
    open,
    statuses: rule.status_codes?.join(', ') ?? '',
    statusInput: '',
    statusInvalid: false,
    keywords: rule.keywords?.join('\n') ?? '',
    keywordInput: '',
    retry: rule.retry,
    effect: rule.effect,
    cooldown: rule.cooldown_seconds === undefined ? '' : String(rule.cooldown_seconds),
  }
}
export function errorRuleValue(row: ErrorRuleDraft): ErrorRule {
  const statuses = row.statuses.trim()
    ? row.statuses
        .split(/[,，\s]+/u)
        .filter(Boolean)
        .map((value) => (/^\d+$/u.test(value) ? Number(value) : Number.NaN))
    : []
  const keywords = row.keywords
    .split(/[\r\n]+/u)
    .map((value) => value.trim())
    .filter(Boolean)
  return {
    ...(statuses.length ? { status_codes: statuses } : {}),
    ...(keywords.length ? { keywords } : {}),
    retry: row.retry,
    effect: row.effect,
    ...(hasErrorRuleCooldown(row.effect)
      ? { cooldown_seconds: /^\d+$/u.test(row.cooldown.trim()) ? Number(row.cooldown) : Number.NaN }
      : {}),
  }
}
export function errorRuleErrors(row: ErrorRuleDraft): Record<string, string> {
  const rule = errorRuleValue(row)
  const errors: Record<string, string> = {}
  if (!rule.status_codes?.length && !rule.keywords?.length) errors.condition = 'condition'
  if (
    row.statusInvalid ||
    rule.status_codes?.some((code) => !Number.isSafeInteger(code) || code < 200 || code > 599)
  )
    errors.statuses = 'statuses'
  if (
    hasErrorRuleCooldown(row.effect) &&
    (!Number.isSafeInteger(rule.cooldown_seconds) ||
      Number(rule.cooldown_seconds) < 1 ||
      Number(rule.cooldown_seconds) > maxErrorRuleCooldown)
  )
    errors.cooldown = 'cooldown'
  return errors
}
export function customErrorRuleLabel(
  ruleID: string | null | undefined,
): { source: string; number: string } | undefined {
  const match = /^custom\.(global|group)\.([1-9]\d*)$/u.exec(ruleID ?? '')
  return match ? { source: match[1]!, number: match[2]! } : undefined
}
