import type {
  AccessKeyCostLimitRuleInput,
  CreateAccessKeyRequest,
  UpdateAccessKeyRequest,
} from '@/app/resources/access-keys'
import type { AccessKeyDto, AccessKeyFiltersDto } from '@/api/control/types'
import { createUUID } from '@/lib/uuid'

import {
  createAccessKeyScopeModes,
  materializeAccessKeyFilters,
  validateAccessKeyScope,
  type AccessKeyScopeModes,
  type GroupCatalogState,
} from './access-key-scope'

export interface AccessKeyDraft {
  name: string
  status: AccessKeyDto['status']
  filters: AccessKeyFiltersDto
  scopeModes: AccessKeyScopeModes
  sourceMode: 'all' | 'restricted'
  expirationMode: 'never' | 'specified'
  expires_at_ms: number | null
  rpm_limit: number
  costLimitRules: AccessKeyCostLimitRuleDraft[]
}

export interface AccessKeyCostLimitRuleDraft extends AccessKeyCostLimitRuleInput {
  clientKey: string
}

function costLimitRuleDraft(rule: AccessKeyCostLimitRuleInput): AccessKeyCostLimitRuleDraft {
  return {
    ...rule,
    clientKey: rule.id === undefined ? createUUID() : `persisted-${rule.id}`,
  }
}

function unique<T>(values: T[]): T[] {
  return [...new Set(values)]
}

export function normalizeAccessKeyFilters(filters: AccessKeyFiltersDto): AccessKeyFiltersDto {
  return {
    groups: unique(filters.groups),
    protocols: unique(filters.protocols),
    models: unique(filters.models.map((value) => value.trim()).filter(Boolean)),
    allowed_cidrs: unique(filters.allowed_cidrs.map((value) => value.trim()).filter(Boolean)),
  }
}

function materializeDraftFilters(draft: AccessKeyDraft): AccessKeyFiltersDto {
  const filters = materializeAccessKeyFilters(draft.filters, draft.scopeModes)
  return normalizeAccessKeyFilters({
    ...filters,
    allowed_cidrs: draft.sourceMode === 'all' ? [] : filters.allowed_cidrs,
  })
}

function expirationValue(draft: AccessKeyDraft): number | null {
  return draft.expirationMode === 'never' ? null : draft.expires_at_ms
}

export function createAccessKeyDraft(accessKey?: AccessKeyDto | null): AccessKeyDraft {
  const filters = normalizeAccessKeyFilters(
    accessKey?.filters ?? { groups: [], protocols: [], models: [], allowed_cidrs: [] },
  )
  return {
    name: accessKey?.name ?? '',
    status: accessKey?.status ?? 'active',
    filters,
    scopeModes: createAccessKeyScopeModes(filters),
    sourceMode: filters.allowed_cidrs.length === 0 ? 'all' : 'restricted',
    expirationMode: accessKey?.expires_at_ms == null ? 'never' : 'specified',
    expires_at_ms: accessKey?.expires_at_ms ?? null,
    rpm_limit: accessKey?.rpm_limit ?? 0,
    costLimitRules: (accessKey?.cost_limit_rules ?? []).map(costLimitRuleDraft),
  }
}

export function createAccessKeyDraftFromCreateInput(input: CreateAccessKeyRequest): AccessKeyDraft {
  const filters = normalizeAccessKeyFilters(input.filters)
  return {
    name: input.name,
    status: input.status,
    filters,
    scopeModes: createAccessKeyScopeModes(filters),
    sourceMode: filters.allowed_cidrs.length === 0 ? 'all' : 'restricted',
    expirationMode: input.expires_at_ms === null ? 'never' : 'specified',
    expires_at_ms: input.expires_at_ms,
    rpm_limit: input.rpm_limit,
    costLimitRules: input.cost_limit_rules.map(costLimitRuleDraft),
  }
}

export function createAccessKeyDraftFromUpdate(
  base: AccessKeyDto,
  patch: UpdateAccessKeyRequest,
): AccessKeyDraft {
  const filters = normalizeAccessKeyFilters(patch.filters ?? base.filters)
  const expiresAt = patch.expires_at_ms !== undefined ? patch.expires_at_ms : base.expires_at_ms
  return {
    name: patch.name ?? base.name,
    status: patch.status ?? base.status,
    filters,
    scopeModes: createAccessKeyScopeModes(filters),
    sourceMode: filters.allowed_cidrs.length === 0 ? 'all' : 'restricted',
    expirationMode: expiresAt === null ? 'never' : 'specified',
    expires_at_ms: expiresAt,
    rpm_limit: patch.rpm_limit ?? base.rpm_limit,
    costLimitRules: (patch.cost_limit_rules ?? base.cost_limit_rules).map(costLimitRuleDraft),
  }
}

export function isAccessKeyDraftValid(
  draft: AccessKeyDraft,
  base?: AccessKeyDto | null,
  groupCatalog: { state: GroupCatalogState; ids: number[] } = {
    state: 'ready',
    ids: [...new Set([...(base?.filters.groups ?? []), ...draft.filters.groups])],
  },
): boolean {
  const expiresAt = expirationValue(draft)
  const expirationValid =
    draft.expirationMode === 'never'
      ? expiresAt === null
      : expiresAt !== null &&
        Number.isSafeInteger(expiresAt) &&
        expiresAt >= 0 &&
        (expiresAt === base?.expires_at_ms || expiresAt > Date.now())
  const allowedCIDRs = normalizeAccessKeyFilters(draft.filters).allowed_cidrs
  return (
    draft.name.trim().length > 0 &&
    Number.isSafeInteger(draft.rpm_limit) &&
    draft.rpm_limit >= 0 &&
    expirationValid &&
    (draft.sourceMode === 'all' || (allowedCIDRs.length > 0 && allowedCIDRs.length <= 64)) &&
    areAccessKeyCostLimitRulesValid(draft.costLimitRules) &&
    validateAccessKeyScope({
      base: base?.filters ?? null,
      filters: draft.filters,
      modes: draft.scopeModes,
      groupCatalog,
    })
  )
}

export function buildCreateAccessKeyInput(draft: AccessKeyDraft): CreateAccessKeyRequest {
  return {
    name: draft.name.trim(),
    status: draft.status,
    filters: materializeDraftFilters(draft),
    expires_at_ms: expirationValue(draft),
    rpm_limit: draft.rpm_limit,
    cost_limit_rules: costLimitInputs(draft.costLimitRules, false),
  }
}

function compareStrings(left: string, right: string): number {
  if (left < right) return -1
  if (left > right) return 1
  return 0
}

function canonicalFilters(filters: AccessKeyFiltersDto): AccessKeyFiltersDto {
  const normalized = normalizeAccessKeyFilters(filters)
  return {
    groups: [...normalized.groups].sort((left, right) => left - right),
    protocols: [...normalized.protocols].sort(compareStrings),
    models: [...normalized.models].sort(compareStrings),
    allowed_cidrs: [...normalized.allowed_cidrs].sort(compareStrings),
  }
}

function equalFilters(left: AccessKeyFiltersDto, right: AccessKeyFiltersDto): boolean {
  return JSON.stringify(canonicalFilters(left)) === JSON.stringify(canonicalFilters(right))
}

function canonicalCostLimitRules(
  rules: readonly AccessKeyCostLimitRuleInput[],
): AccessKeyCostLimitRuleInput[] {
  return rules
    .map((rule) => ({
      ...(rule.id === undefined ? {} : { id: rule.id }),
      kind: rule.kind,
      limit_usd: rule.limit_usd,
      ...(rule.kind === 'periodic' ? { period_seconds: rule.period_seconds } : {}),
    }))
    .sort((left, right) => {
      if (left.kind !== right.kind) return left.kind === 'total' ? -1 : 1
      return (
        (left.period_seconds ?? 0) - (right.period_seconds ?? 0) ||
        (left.id ?? Number.MAX_SAFE_INTEGER) - (right.id ?? Number.MAX_SAFE_INTEGER) ||
        left.limit_usd.localeCompare(right.limit_usd)
      )
    })
}

function equalCostLimitRules(
  left: readonly AccessKeyCostLimitRuleInput[],
  right: readonly AccessKeyCostLimitRuleInput[],
): boolean {
  return (
    JSON.stringify(canonicalCostLimitRules(left)) === JSON.stringify(canonicalCostLimitRules(right))
  )
}

function costLimitInputs(
  rules: readonly AccessKeyCostLimitRuleDraft[],
  includeIDs: boolean,
): AccessKeyCostLimitRuleInput[] {
  return canonicalCostLimitRules(
    rules.map((rule) => ({
      ...(includeIDs && rule.id !== undefined ? { id: rule.id } : {}),
      kind: rule.kind,
      limit_usd: rule.limit_usd,
      ...(rule.kind === 'periodic' ? { period_seconds: rule.period_seconds } : {}),
    })),
  )
}

export function areAccessKeyCostLimitRulesValid(
  rules: readonly AccessKeyCostLimitRuleDraft[],
): boolean {
  let totalCount = 0
  let periodicCount = 0
  const periods = new Set<number>()
  const ids = new Set<number>()
  for (const rule of rules) {
    if (!/^(?:0|[1-9]\d*)(?:\.\d{1,9})?$/.test(rule.limit_usd)) return false
    if (/^0(?:\.0+)?$/.test(rule.limit_usd)) return false
    if (rule.id !== undefined) {
      if (!Number.isSafeInteger(rule.id) || rule.id < 1 || ids.has(rule.id)) return false
      ids.add(rule.id)
    }
    if (rule.kind === 'total') {
      totalCount += 1
      if (rule.period_seconds !== undefined && rule.period_seconds !== 0) return false
      continue
    }
    periodicCount += 1
    const period = rule.period_seconds
    if (
      period === undefined ||
      !Number.isSafeInteger(period) ||
      period < 60 ||
      period > 31_536_000 ||
      periods.has(period)
    ) {
      return false
    }
    periods.add(period)
  }
  return totalCount <= 1 && periodicCount <= 10
}

export function isAccessKeyDraftDirty(draft: AccessKeyDraft, base?: AccessKeyDto | null): boolean {
  const initial = createAccessKeyDraft(base)
  if (
    draft.scopeModes.groups !== initial.scopeModes.groups ||
    draft.scopeModes.protocols !== initial.scopeModes.protocols ||
    draft.scopeModes.models !== initial.scopeModes.models
  ) {
    return true
  }
  if (draft.sourceMode !== initial.sourceMode || draft.expirationMode !== initial.expirationMode) {
    return true
  }
  if (base) return Object.keys(buildAccessKeyUpdatePatch(base, draft)).length > 0
  return (
    draft.name !== initial.name ||
    draft.status !== initial.status ||
    draft.expires_at_ms !== initial.expires_at_ms ||
    draft.rpm_limit !== initial.rpm_limit ||
    !equalFilters(draft.filters, initial.filters) ||
    !equalCostLimitRules(
      costLimitInputs(draft.costLimitRules, false),
      costLimitInputs(initial.costLimitRules, false),
    )
  )
}

export function accessKeyMatchesUpdatePatch(
  accessKey: AccessKeyDto,
  patch: UpdateAccessKeyRequest,
  base: AccessKeyDto,
): boolean {
  return (
    (patch.name === undefined || patch.name === accessKey.name) &&
    (patch.status === undefined || patch.status === accessKey.status) &&
    (patch.filters === undefined || equalFilters(patch.filters, accessKey.filters)) &&
    (patch.expires_at_ms === undefined || patch.expires_at_ms === accessKey.expires_at_ms) &&
    (patch.rpm_limit === undefined || patch.rpm_limit === accessKey.rpm_limit) &&
    (patch.cost_limit_rules === undefined ||
      costLimitRulesMatchReconciliation(
        accessKey.cost_limit_rules,
        patch.cost_limit_rules,
        base.cost_limit_rules,
      ))
  )
}

function normalizedUSD(value: string): string {
  const [whole, fraction = ''] = value.split('.')
  const normalizedFraction = fraction.replace(/0+$/, '')
  return normalizedFraction.length > 0 ? `${whole}.${normalizedFraction}` : whole
}

function sameCostLimitRuleValue(
  left: AccessKeyCostLimitRuleInput,
  right: AccessKeyCostLimitRuleInput,
): boolean {
  return (
    left.kind === right.kind &&
    (left.period_seconds ?? 0) === (right.period_seconds ?? 0) &&
    normalizedUSD(left.limit_usd) === normalizedUSD(right.limit_usd)
  )
}

function costLimitRulesMatchReconciliation(
  latest: readonly AccessKeyCostLimitRuleInput[],
  desired: readonly AccessKeyCostLimitRuleInput[],
  base: readonly AccessKeyCostLimitRuleInput[],
): boolean {
  if (latest.length !== desired.length || latest.some((rule) => rule.id === undefined)) return false

  const latestByID = new Map(latest.map((rule) => [rule.id!, rule]))
  const baseIDs = new Set(base.flatMap((rule) => (rule.id === undefined ? [] : [rule.id])))
  const matchedIDs = new Set<number>()
  for (const desiredRule of desired) {
    let matched: AccessKeyCostLimitRuleInput | undefined
    if (desiredRule.id !== undefined) {
      matched = latestByID.get(desiredRule.id)
    } else {
      matched = latest.find(
        (candidate) =>
          candidate.id !== undefined &&
          !baseIDs.has(candidate.id) &&
          !matchedIDs.has(candidate.id) &&
          sameCostLimitRuleValue(candidate, desiredRule),
      )
    }
    if (
      matched?.id === undefined ||
      matchedIDs.has(matched.id) ||
      !sameCostLimitRuleValue(matched, desiredRule)
    ) {
      return false
    }
    matchedIDs.add(matched.id)
  }
  return matchedIDs.size === latest.length
}

export function buildAccessKeyUpdatePatch(
  base: AccessKeyDto,
  draft: AccessKeyDraft,
): UpdateAccessKeyRequest {
  const patch: UpdateAccessKeyRequest = {}
  const name = draft.name.trim()
  const filters = materializeDraftFilters(draft)
  const expiresAt = expirationValue(draft)
  if (name !== base.name) patch.name = name
  if (draft.status !== base.status) patch.status = draft.status
  if (!equalFilters(filters, base.filters)) patch.filters = filters
  if (expiresAt !== base.expires_at_ms) patch.expires_at_ms = expiresAt
  if (draft.rpm_limit !== base.rpm_limit) patch.rpm_limit = draft.rpm_limit
  const desiredCostLimits = costLimitInputs(draft.costLimitRules, true)
  if (!equalCostLimitRules(desiredCostLimits, base.cost_limit_rules)) {
    patch.cost_limit_rules = desiredCostLimits
  }
  return patch
}
