import { InvalidResponseError } from '@shared/http/errors'
import { boolean, integer, list, oneOf, record, text } from './response'

const planLevels = ['free', 'standard', 'premium', 'elite'] as const

export interface QuotaUsage {
  from?: number
  to?: number
  requests: number
  tokens: number
  cost: string
  complete: boolean
  usageComplete: boolean
  pricingComplete: boolean
}
export interface CredentialQuota {
  id: string
  label: string
  labelKey?: string
  scope: string
  unit: string
  used?: number
  limit?: number
  remaining?: number
  utilization?: number
  resetsAt?: number
  windowSeconds?: number
  models: string[]
  state: 'available' | 'exhausted' | 'unknown'
  usage?: QuotaUsage
}
export interface CredentialObservation {
  state: 'fresh' | 'stale' | 'refreshing' | 'error' | 'unavailable'
  observedAt: number | null
  error?: string
  plan: string
  planLevel?: 'free' | 'standard' | 'premium' | 'elite'
  organization: string
  windows: CredentialQuota[]
  account?: {
    name: string
    seat: string
    billing: string
    organizationRole: string
    workspaceRole: string
    rateLimitTier: string
    extraUsage?: boolean
  }
  resetCredits: number
  creditExpirations: (number | undefined)[]
}
function amount(value: unknown): number | undefined {
  if (value === undefined) return undefined
  if (typeof value !== 'number' || !Number.isFinite(value) || value < 0)
    throw new InvalidResponseError()
  return value
}
function optionalTime(value: unknown): number | undefined {
  return value === undefined ? undefined : integer(value)
}
export function readObservation(value: unknown): CredentialObservation | undefined {
  if (value == null) return undefined
  const data = record(value)
  const snapshot = data.snapshot == null ? undefined : record(data.snapshot)
  const plan = snapshot ? record(snapshot.plan_summary) : undefined
  const account = snapshot?.account_summary == null ? undefined : record(snapshot.account_summary)
  return {
    state: oneOf(data.state, ['fresh', 'stale', 'refreshing', 'error', 'unavailable']),
    observedAt: data.observed_at_ms == null ? null : integer(data.observed_at_ms),
    error: data.last_error_code === undefined ? undefined : text(data.last_error_code),
    plan: plan?.name == null ? '' : text(plan.name),
    planLevel: planLevels.find((level) => level === plan?.level),
    organization: account?.organization_name == null ? '' : text(account.organization_name),
    account: account
      ? {
          name: account.display_name == null ? '' : text(account.display_name),
          seat: account.seat_tier == null ? '' : text(account.seat_tier),
          billing: account.billing_type == null ? '' : text(account.billing_type),
          organizationRole:
            account.organization_role == null ? '' : text(account.organization_role),
          workspaceRole: account.workspace_role == null ? '' : text(account.workspace_role),
          rateLimitTier:
            account.organization_rate_limit_tier == null
              ? account.user_rate_limit_tier == null
                ? ''
                : text(account.user_rate_limit_tier)
              : text(account.organization_rate_limit_tier),
          extraUsage:
            account.extra_usage_enabled == null ? undefined : boolean(account.extra_usage_enabled),
        }
      : undefined,
    resetCredits:
      snapshot?.reset_credits_available === undefined
        ? 0
        : integer(snapshot.reset_credits_available),
    creditExpirations:
      snapshot?.reset_credits === undefined
        ? []
        : list(snapshot.reset_credits).map((value) => optionalTime(record(value).expires_at_ms)),
    windows: snapshot
      ? list(snapshot.quota_windows).map((value): CredentialQuota => {
          const window = record(value)
          const usage =
            window.observed_usage === undefined ? undefined : record(window.observed_usage)
          const cost = usage ? text(usage.estimated_reference_cost_nano_usd) : '0'
          if (!/^\d+$/u.test(cost)) throw new InvalidResponseError()
          return {
            id: text(window.id),
            label: text(window.label),
            labelKey: window.label_key === undefined ? undefined : text(window.label_key),
            scope: text(window.scope),
            unit: text(window.unit),
            used: amount(window.used),
            limit: amount(window.limit),
            remaining: amount(window.remaining),
            utilization: amount(window.utilization),
            resetsAt: optionalTime(window.reset_at_ms),
            windowSeconds: optionalTime(window.window_seconds),
            models: window.model_ids === undefined ? [] : list(window.model_ids).map(text),
            state: oneOf(window.state, ['available', 'exhausted', 'unknown']),
            usage: usage
              ? {
                  from: optionalTime(usage.window_start_ms),
                  to: optionalTime(usage.window_end_ms),
                  requests: integer(usage.request_count),
                  tokens: integer(usage.total_tokens),
                  cost,
                  complete: boolean(usage.data_complete),
                  usageComplete: boolean(usage.usage_complete),
                  pricingComplete: boolean(usage.pricing_complete),
                }
              : undefined,
          }
        })
      : [],
  }
}
