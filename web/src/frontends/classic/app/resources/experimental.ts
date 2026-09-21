import {
  projectBoolean as boolean,
  projectSafeInteger,
  projectArray,
  projectEnum as oneOf,
  projectRecord as record,
  projectString,
} from './projector'
const text = (value: unknown) => projectString(value, { allowEmpty: true })
const integer = (value: unknown, minimum = 0) => projectSafeInteger(value, { minimum })
const list = (value: unknown) => projectArray(value, (item) => item)
import { InvalidResponseError } from '@shared/http/errors'

export interface JevConfig {
  model: string
  group_id: number
  timeout_seconds: number
}
export interface AuditRule {
  id: string
  name: string
  enabled: boolean
  instructions: string
  action: 'block' | 'warn'
  threshold: number
}
export interface AuditConfig {
  enabled: boolean
  access_key_ids: number[]
  rules: AuditRule[]
}
export interface DecisionRoute {
  group_id: number
  group_name: string
  models: string[]
}
export interface AuditAccessKey {
  id: number
  name: string
}
export const defaultJev = (): JevConfig => ({ model: '', group_id: 0, timeout_seconds: 2 })
export const defaultAudit = (): AuditConfig => ({
  enabled: false,
  access_key_ids: [],
  rules: [],
})
export function readJev(value: unknown): JevConfig {
  if (value === undefined) return defaultJev()
  const row = record(value)
  return {
    model: text(row.model),
    group_id: integer(row.group_id),
    timeout_seconds: integer(row.timeout_seconds, 1),
  }
}
export function readAudit(value: unknown): AuditConfig {
  if (value === undefined) return defaultAudit()
  const row = record(value)
  return {
    enabled: boolean(row.enabled),
    access_key_ids: list(row.access_key_ids).map((id) => integer(id, 1)),
    rules: list(row.rules).map((value) => {
      const rule = record(value)
      if (typeof rule.threshold !== 'number' || !Number.isFinite(rule.threshold))
        throw new InvalidResponseError()
      return {
        id: text(rule.id),
        name: text(rule.name),
        enabled: boolean(rule.enabled),
        action: oneOf(rule.action, ['block', 'warn']),
        instructions: text(rule.instructions),
        threshold: rule.threshold,
      }
    }),
  }
}
export function readDecisionRoutes(value: unknown): DecisionRoute[] {
  return list(value).map((value) => {
    const row = record(value)
    return {
      group_id: integer(row.group_id, 1),
      group_name: text(row.group_name),
      models: list(row.models).map(text),
    }
  })
}
export function readAuditAccessKeys(value: unknown): AuditAccessKey[] {
  return list(value).map((value) => {
    const row = record(value)
    return { id: integer(row.id, 1), name: text(row.name) }
  })
}
export function validJev(value: JevConfig): boolean {
  return (
    Number.isInteger(value.timeout_seconds) &&
    value.timeout_seconds >= 1 &&
    value.timeout_seconds <= 60
  )
}
export function validAudit(value: AuditConfig): boolean {
  return (
    (!value.enabled || value.rules.some((r) => r.enabled)) &&
    value.rules.length <= 16 &&
    new Set(value.rules.map((r) => r.id)).size === value.rules.length &&
    value.rules.every(
      (r) =>
        /^[a-zA-Z][a-zA-Z0-9_-]{0,63}$/u.test(r.id) &&
        r.name.trim() &&
        new TextEncoder().encode(r.name).length <= 128 &&
        r.instructions.trim() &&
        new TextEncoder().encode(r.instructions).length <= 4096 &&
        typeof r.enabled === 'boolean' &&
        ['block', 'warn'].includes(r.action) &&
        r.threshold > 0 &&
        r.threshold <= 1,
    )
  )
}

export interface AuditResult {
  status: string
  reason: string
  findings: { rule_id: string; name: string; action: string }[]
  calls: {
    model: string
    group_name: string
    called: boolean
    estimated_cost_nano_usd: string
    cost_state: string
    pricing_completeness: string
  }[]
}
export function readAuditResult(value: unknown): AuditResult {
  const row = record(value)
  return {
    status: text(row.status),
    reason: row.reason === undefined ? '' : text(row.reason),
    findings: list(row.findings).map((value) => {
      const f = record(value)
      return { rule_id: text(f.rule_id), name: text(f.name), action: text(f.action) }
    }),
    calls: list(row.calls).map((value) => {
      const c = record(value)
      const amount = text(c.estimated_cost_nano_usd)
      if (!/^[0-9]+$/u.test(amount)) throw new InvalidResponseError()
      return {
        model: text(c.model),
        group_name: text(c.group_name),
        called: boolean(c.called),
        estimated_cost_nano_usd: amount,
        cost_state: text(c.cost_state),
        pricing_completeness: text(c.pricing_completeness),
      }
    }),
  }
}
