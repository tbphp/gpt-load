import { boolean, integer, list, oneOf, record, text } from './response'
import { InvalidResponseError } from '@shared/http/errors'

export interface JevConfig {
  model: string
  group_id: number
  timeout_seconds: number
}
export interface AuditRule {
  id: string
  name: string
  instructions: string
  threshold: number
}
export interface AuditConfig {
  enabled: boolean
  mode: 'observe' | 'enforce'
  access_key_ids: number[]
  local_secrets: boolean
  semantic_enabled: boolean
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
  mode: 'observe',
  access_key_ids: [],
  local_secrets: true,
  semantic_enabled: false,
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
    mode: oneOf(row.mode, ['observe', 'enforce']),
    access_key_ids: list(row.access_key_ids).map((id) => integer(id, 1)),
    local_secrets: boolean(row.local_secrets),
    semantic_enabled: boolean(row.semantic_enabled),
    rules: list(row.rules).map((value) => {
      const rule = record(value)
      if (typeof rule.threshold !== 'number' || !Number.isFinite(rule.threshold))
        throw new InvalidResponseError()
      return {
        id: text(rule.id),
        name: text(rule.name),
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
    (!value.enabled || value.local_secrets || value.semantic_enabled) &&
    (!value.semantic_enabled || value.rules.length > 0) &&
    value.rules.length <= 16 &&
    value.rules.every(
      (r) => r.name.trim() && r.instructions.trim() && r.threshold > 0.5 && r.threshold <= 1,
    )
  )
}

export interface AuditResult {
  mode: string
  status: string
  reason: string
  checks: number
  duration_ms: number
  findings: {
    rule_id: string
    name: string
    source: string
    status: string
    probability?: number
  }[]
  calls: {
    model: string
    group_name: string
    called: boolean
    reason: string
    duration_ms: number
    estimated_cost_nano_usd: string
    cost_state: string
    pricing_completeness: string
  }[]
}
export function readAuditResult(value: unknown): AuditResult {
  const row = record(value)
  return {
    mode: text(row.mode),
    status: text(row.status),
    reason: row.reason === undefined ? '' : text(row.reason),
    checks: integer(row.checks),
    duration_ms: integer(row.duration_ms),
    findings: list(row.findings).map((value) => {
      const f = record(value)
      if (
        f.probability !== undefined &&
        (typeof f.probability !== 'number' ||
          !Number.isFinite(f.probability) ||
          f.probability < 0 ||
          f.probability > 1)
      )
        throw new InvalidResponseError()
      return {
        rule_id: text(f.rule_id),
        name: text(f.name),
        source: text(f.source),
        status: text(f.status),
        probability: f.probability as number | undefined,
      }
    }),
    calls: list(row.calls).map((value) => {
      const c = record(value)
      const amount = text(c.estimated_cost_nano_usd)
      if (!/^[0-9]+$/u.test(amount)) throw new InvalidResponseError()
      return {
        model: text(c.model),
        group_name: text(c.group_name),
        called: boolean(c.called),
        reason: c.reason === undefined ? '' : text(c.reason),
        duration_ms: integer(c.duration_ms),
        estimated_cost_nano_usd: amount,
        cost_state: text(c.cost_state),
        pricing_completeness: text(c.pricing_completeness),
      }
    }),
  }
}
