import { credentialDisplayText } from '@shared/credential-display'
import { codexLiveModes, type CodexLiveMode } from '@shared/codex-live'
import type { ApiClient } from '@shared/http/client'
import { ApiError, InvalidResponseError } from '@shared/http/errors'
import { boolean, integer, list, oneOf, record, text } from './response'
import { readModelCandidates } from './model-discovery'
import type { ProxyOverride } from './group-create'
import { sortProtocols } from '@modern/i18n/protocols'
import { readObservation, type CredentialObservation } from './credential-observation'
import { readGroupBasics, type GroupBasics } from './groups'

export const groupSettingsKey = (id: number) => ['modern', 'group-settings', id] as const
export const groupPolicyKey = (id: number) => ['modern', 'group-policy', id] as const
export const credentialPolicyKey = (groupId: number, credentialId: number) =>
  ['modern', 'credential-policy', groupId, credentialId] as const
export const policyDiscoveryKey = () => ['modern', 'policy-discovery'] as const
export const groupModelsKey = (id: number) => ['modern', 'group-models', id] as const
export const groupCredentialsKey = (id: number) => ['modern', 'group-credentials', id] as const
export const credentialStates = ['available', 'cooldown', 'blacklisted', 'disabled'] as const
export type CredentialState = (typeof credentialStates)[number]
export const credentialSorts = [
  'priority',
  'newest',
  'oldest',
  'name',
  'weight_desc',
  'weight_asc',
  'failures',
  'rpm_peak_desc',
] as const
export interface CredentialFilters {
  credential?: string
  q: string
  status: string
  page: number
  pageSize: number
  sort: (typeof credentialSorts)[number]
  proxy: '' | 'inherit' | 'direct' | 'custom'
  reset: '' | 'available' | 'none' | 'unknown'
}
export const runtimeNumbers = [
  'concurrency_limit',
  'first_byte_timeout',
  'request_timeout',
  'stream_idle_timeout',
  'blacklist_threshold',
] as const
export const runtimeSwitches = [
  'affinity_enabled',
  'responses_websocket_enabled',
  'empty_response_retry',
] as const
export type RuntimeNumber = (typeof runtimeNumbers)[number]
export type RuntimeSwitch = (typeof runtimeSwitches)[number]
export interface HeaderRules {
  set: Record<string, string>
  remove: string[]
}
export interface ParameterRule {
  match: { protocol?: string; model?: string }
  set?: Record<string, unknown>
  remove?: string[]
}
export type RuntimeSettings = Partial<
  Record<RuntimeNumber, number> & Record<RuntimeSwitch, boolean>
> & {
  codex_live_mode?: CodexLiveMode
  header_rules?: HeaderRules
  parameter_overrides?: ParameterRule[]
}
export interface GroupSettings extends GroupBasics {
  channelID: string
  params: Record<string, string>
  validationModel: string | null
  validationProtocol: string | null
  validationProtocols: string[]
  overrides: RuntimeSettings
  effective: Required<Omit<RuntimeSettings, 'parameter_overrides'>>
  proxy: {
    id?: number
    name?: string
    referenceState?: string
    mode: 'inherit' | 'direct' | 'custom'
    display: string
    hasAuth: boolean
  }
}
export interface AdvancedSettingsPatch {
  params?: Record<string, string>
  validation_model?: string | null
  validation_protocol?: string
  overrides?: RuntimeSettings
  proxy?: ProxyOverride | null
}
function stringMap(value: unknown): Record<string, string> {
  return Object.fromEntries(Object.entries(record(value)).map(([key, value]) => [key, text(value)]))
}
function readRuntime(value: unknown): RuntimeSettings {
  const raw = record(value)
  const result: RuntimeSettings = {}
  for (const key of runtimeNumbers) if (raw[key] !== undefined) result[key] = integer(raw[key])
  for (const key of runtimeSwitches) if (raw[key] !== undefined) result[key] = boolean(raw[key])
  if (raw.codex_live_mode !== undefined)
    result.codex_live_mode = oneOf(raw.codex_live_mode, codexLiveModes)
  if (raw.header_rules !== undefined) {
    const headers = record(raw.header_rules)
    result.header_rules = { set: stringMap(headers.set), remove: list(headers.remove).map(text) }
  }
  if (raw.parameter_overrides !== undefined) {
    result.parameter_overrides = list(raw.parameter_overrides).map((value) => {
      const rule = record(value)
      const match = record(rule.match)
      return {
        match: {
          ...(match.protocol === undefined ? {} : { protocol: text(match.protocol) }),
          ...(match.model === undefined ? {} : { model: text(match.model) }),
        },
        ...(rule.set === undefined ? {} : { set: record(rule.set) }),
        ...(rule.remove === undefined ? {} : { remove: list(rule.remove).map(text) }),
      }
    })
  }
  return result
}
function readSettings(value: unknown): GroupSettings {
  const data = record(value)
  const proxy = record(data.proxy)
  const effective = readRuntime(data.effective)
  if (
    [...runtimeNumbers, ...runtimeSwitches, 'codex_live_mode', 'header_rules'].some(
      (key) => effective[key as keyof RuntimeSettings] === undefined,
    )
  )
    throw new InvalidResponseError()
  return {
    ...readGroupBasics(data),
    channelID: text(data.channel_id),
    params: stringMap(data.params),
    validationModel: data.validation_model === null ? null : text(data.validation_model),
    validationProtocol: data.validation_protocol === null ? null : text(data.validation_protocol),
    validationProtocols: sortProtocols(list(data.validation_protocols).map(text)),
    overrides: readRuntime(data.overrides),
    effective: effective as GroupSettings['effective'],
    proxy: {
      id: proxy.proxy_id === undefined ? undefined : integer(proxy.proxy_id, 1),
      name: proxy.proxy_name === undefined ? undefined : text(proxy.proxy_name),
      referenceState: proxy.reference_state === undefined ? undefined : text(proxy.reference_state),
      mode: oneOf(proxy.configured_mode, ['inherit', 'direct', 'custom']),
      display: proxy.display_url === undefined ? '' : text(proxy.display_url),
      hasAuth: boolean(proxy.has_auth),
    },
  }
}
export async function getGroupSettings(client: ApiClient, id: number, signal: AbortSignal) {
  return readSettings(await client.request(`/api/groups/${id}/settings`, { signal }))
}
export async function saveGroupSettings(
  client: ApiClient,
  id: number,
  patch: AdvancedSettingsPatch,
  signal: AbortSignal,
) {
  return readSettings(
    await client.request(`/api/groups/${id}/settings`, { method: 'PUT', json: patch, signal }),
  )
}

export interface ChannelSwitchConflict {
  groups: { id: number; name: string }[]
}
export class ChannelSwitchConflictError extends Error {
  constructor(readonly conflict: ChannelSwitchConflict) {
    super('channel target conflict')
  }
}
// 切换渠道只提交目标渠道；参数由后端按目标渠道的字段重新推导。
export async function switchGroupChannel(
  client: ApiClient,
  id: number,
  channelID: string,
  confirmSameTarget: boolean,
  signal: AbortSignal,
) {
  try {
    return readSettings(
      await client.request(`/api/groups/${id}/channel`, {
        method: 'PUT',
        json: { channel_id: channelID, confirm_same_target: confirmSameTarget },
        signal,
      }),
    )
  } catch (error) {
    const conflict = readChannelSwitchConflict(error)
    if (conflict) throw new ChannelSwitchConflictError(conflict)
    throw error
  }
}
function readChannelSwitchConflict(error: unknown): ChannelSwitchConflict | undefined {
  if (!(error instanceof ApiError) || error.code !== 'CHANNEL_TARGET_CONFLICT') return undefined
  try {
    return {
      groups: list(record(error.data).groups).map((value) => {
        const group = record(value)
        return { id: integer(group.id), name: text(group.name) }
      }),
    }
  } catch {
    return { groups: [] }
  }
}

export interface GroupModel {
  id: string
  alias: string
  clientModel: string
  pricingStatus: 'pending' | 'configured'
}
function readModels(value: unknown): GroupModel[] {
  return list(record(value).items).map((value) => {
    const model = record(value)
    return {
      id: text(model.id),
      alias: text(model.alias),
      clientModel: text(model.client_model),
      pricingStatus: oneOf(model.pricing_status, ['pending', 'configured']),
    }
  })
}
export async function getGroupModels(client: ApiClient, id: number, signal: AbortSignal) {
  return readModels(await client.request(`/api/groups/${id}/models`, { signal }))
}
export async function saveGroupModels(
  client: ApiClient,
  id: number,
  models: { id: string; alias: string }[],
  signal: AbortSignal,
) {
  return readModels(
    await client.request(`/api/groups/${id}/models`, {
      method: 'PUT',
      signal,
      json: {
        models: models.map((model) => ({
          id: model.id.trim(),
          alias: model.alias.trim(),
          alias_enabled: Boolean(model.alias.trim()),
        })),
      },
    }),
  )
}
export async function discoverGroupModels(client: ApiClient, id: number, signal: AbortSignal) {
  const data = record(
    await client.request(`/api/groups/${id}/models/discover`, { method: 'POST', signal }),
  )
  return readModelCandidates(data.models)
}

export interface CredentialRow {
  name: string
  label: string
  rpmPeakHour?: number
  id: number
  mask: string
  account: string
  state: CredentialState
  enabled: boolean
  weight: number
  weightManual?: number | null
  successes: number
  failures: number
  failuresInRow: number
  lastUsed: number | null
  cooldownUntil: number | null
  daily: { successes: number; failures: number; complete: boolean } | null
  authState: 'ready' | 'refreshing' | 'reauthorization_required' | 'outcome_unknown'
  secretVersion: number
  expiresAt?: number
  lastRefresh?: number
  authError?: string
  failureCategory: string
  lastStatusCode: number | null
  modelCooldowns: { model: string; until: number }[]
  recovery: {
    mode: 'none' | 'cooldown' | 'probe' | 'manual'
    automatic: boolean
    at: number | null
  }
  proxy: {
    id?: number
    name?: string
    referenceState?: string
    mode: 'inherit' | 'direct' | 'custom'
    source: string
    display: string
  }
  observation?: CredentialObservation
}
export interface CredentialCollection {
  items: CredentialRow[]
  counts: Record<CredentialState | 'total', number>
  total: number
  page: number
  pageSize: number
}
export function readCredential(value: unknown): CredentialRow {
  const row = record(value)
  const account = row.account == null ? null : record(row.account)
  const daily = row.daily_usage == null ? null : record(row.daily_usage)
  const proxy = record(row.proxy)
  const recovery = record(row.recovery)
  return {
    id: integer(row.credential_id, 1),
    rpmPeakHour: row.rpm_peak_hour == null ? undefined : integer(row.rpm_peak_hour),
    name: text(row.name ?? ''),
    label: credentialDisplayText(
      text(row.name ?? ''),
      account ? text(account.email ?? account.email_mask ?? '') || text(row.mask) : text(row.mask),
      text(row.connection_type),
    ),
    mask: text(row.mask),
    account: account ? text(account.email ?? account.email_mask ?? '') : '',
    state: oneOf(row.effective_status, credentialStates),
    enabled: oneOf(row.configured_status, ['active', 'disabled']) === 'active',
    weight: integer(row.weight),
    weightManual:
      row.weight_manual === undefined
        ? undefined
        : row.weight_manual === null
          ? null
          : integer(row.weight_manual),
    successes: integer(row.recent_success_count),
    failures: integer(row.recent_failure_count),
    failuresInRow: integer(row.consecutive_failure_count),
    lastUsed: row.last_used_at_ms == null ? null : integer(row.last_used_at_ms),
    cooldownUntil: row.cooldown_until_ms == null ? null : integer(row.cooldown_until_ms),
    daily: daily
      ? {
          successes: integer(daily.success_count),
          failures: integer(daily.failure_count),
          complete: boolean(daily.data_complete),
        }
      : null,
    authState: oneOf(row.auth_state, [
      'ready',
      'refreshing',
      'reauthorization_required',
      'outcome_unknown',
    ]),
    secretVersion: integer(row.secret_version, 1),
    expiresAt: account?.expires_at_ms === undefined ? undefined : integer(account.expires_at_ms),
    lastRefresh:
      account?.last_refresh_at_ms === undefined ? undefined : integer(account.last_refresh_at_ms),
    authError: row.auth_error_code === undefined ? undefined : text(row.auth_error_code),
    failureCategory: text(row.last_failure_category),
    lastStatusCode: row.last_status_code === null ? null : integer(row.last_status_code),
    modelCooldowns: list(row.model_cooldowns).map((value) => {
      const cooldown = record(value)
      return { model: text(cooldown.model), until: integer(cooldown.cooldown_until_ms) }
    }),
    recovery: {
      mode: oneOf(recovery.mode, ['none', 'cooldown', 'probe', 'manual']),
      automatic: boolean(recovery.automatic),
      at: recovery.at_ms === null ? null : integer(recovery.at_ms),
    },
    proxy: {
      id: proxy.proxy_id === undefined ? undefined : integer(proxy.proxy_id, 1),
      name: proxy.proxy_name === undefined ? undefined : text(proxy.proxy_name),
      referenceState: proxy.reference_state === undefined ? undefined : text(proxy.reference_state),
      mode: oneOf(proxy.configured_mode, ['inherit', 'direct', 'custom']),
      source: text(proxy.effective_source),
      display: proxy.display_url === undefined ? '' : text(proxy.display_url),
    },
    observation: readObservation(row.observation),
  }
}
export async function getGroupCredentials(
  client: ApiClient,
  id: number,
  filters: CredentialFilters,
  signal: AbortSignal,
): Promise<CredentialCollection> {
  const params = new URLSearchParams({
    page: String(filters.page),
    page_size: String(filters.pageSize),
  })
  if (filters.q.trim()) params.set('q', filters.q.trim())
  if (filters.credential) params.set('credential_key', filters.credential)
  if (filters.status) params.set('status', filters.status)
  if (filters.sort !== 'priority') params.set('sort', filters.sort)
  if (filters.proxy) params.set('proxy', filters.proxy)
  if (filters.reset) params.set('reset', filters.reset)
  const data = record(
    await client.request(`/api/modern/groups/${id}/credentials?${params}`, { signal }),
  )
  const summary = record(data.summary)
  const pagination = record(data.pagination)
  return {
    items: list(data.items).map(readCredential),
    counts: {
      total: integer(summary.total),
      available: integer(summary.available),
      cooldown: integer(summary.cooldown),
      blacklisted: integer(summary.blacklisted),
      disabled: integer(summary.disabled),
    },
    total: integer(pagination.total_items),
    page: integer(pagination.page, 1),
    pageSize: integer(pagination.page_size, 1),
  }
}
export async function setCredentialEnabled(
  client: ApiClient,
  groupID: number,
  id: number,
  enabled: boolean,
  signal: AbortSignal,
) {
  return readCredential(
    await client.request(`/api/groups/${groupID}/credentials/${id}`, {
      method: 'PUT',
      json: { status: enabled ? 'active' : 'disabled' },
      signal,
    }),
  )
}
export async function batchGroupCredentials(
  client: ApiClient,
  groupID: number,
  ids: number[],
  action: 'enable' | 'disable' | 'delete',
  signal: AbortSignal,
) {
  const data = record(
    await client.request(`/api/groups/${groupID}/credentials/batch`, {
      method: 'POST',
      json: { action, credential_ids: ids },
      signal,
    }),
  )
  return list(data.affected_credential_ids).map((id) => integer(id, 1))
}

export async function batchAllGroupCredentials(
  client: ApiClient,
  groupID: number,
  action: 'enable' | 'disable' | 'restore',
  signal: AbortSignal,
) {
  const data = record(
    await client.request(`/api/groups/${groupID}/credentials/batch`, {
      method: 'POST',
      json: { action, scope: 'all' },
      signal,
    }),
  )
  return list(data.affected_credential_ids).map((id) => integer(id, 1))
}

export interface GroupPolicy {
  scope: 'group'
  id: number
  groupId: number
  revisionText: string
  configText: string
}

export interface CredentialPolicy {
  scope: 'credential'
  id: number
  groupId: number
  credentialId: number
  revisionText: string
  configText: string
}

const MAX_CANONICAL_UINT64 = 18446744073709551615n

function isValidCanonicalUint64(s: string): boolean {
  if (typeof s !== 'string') return false
  if (!/^(?:0|[1-9]\d{0,19})$/.test(s)) return false
  try {
    const b = BigInt(s)
    return b >= 0n && b <= MAX_CANONICAL_UINT64
  } catch {
    return false
  }
}

export function readGroupPolicy(raw: unknown): GroupPolicy {
  const data = record(raw)
  const scope = oneOf(data.scope, ['group'] as const)
  const id = integer(data.id, 1)
  const groupId = integer(data.group_id, 1)
  if (id !== groupId) {
    throw new InvalidResponseError()
  }
  const revisionText = text(data.revision_text)
  if (!isValidCanonicalUint64(revisionText)) {
    throw new InvalidResponseError()
  }
  const configText = text(data.config_text)

  return {
    scope,
    id,
    groupId,
    revisionText,
    configText,
  }
}

export function readCredentialPolicy(raw: unknown): CredentialPolicy {
  const data = record(raw)
  const scope = oneOf(data.scope, ['credential'] as const)
  const id = integer(data.id, 1)
  const groupId = integer(data.group_id, 1)
  const credentialId = integer(data.credential_id, 1)
  if (id !== credentialId) {
    throw new InvalidResponseError()
  }
  const revisionText = text(data.revision_text)
  if (!isValidCanonicalUint64(revisionText)) {
    throw new InvalidResponseError()
  }
  const configText = text(data.config_text)

  return {
    scope,
    id,
    groupId,
    credentialId,
    revisionText,
    configText,
  }
}

export async function getGroupPolicy(
  client: ApiClient,
  id: number,
  signal: AbortSignal,
): Promise<GroupPolicy> {
  return readGroupPolicy(await client.request(`/api/groups/${id}/policy`, { signal }))
}

export async function getCredentialPolicy(
  client: ApiClient,
  groupId: number,
  credentialId: number,
  signal: AbortSignal,
): Promise<CredentialPolicy> {
  return readCredentialPolicy(
    await client.request(`/api/groups/${groupId}/credentials/${credentialId}/policy`, { signal }),
  )
}

export async function saveGroupPolicy(
  client: ApiClient,
  id: number,
  expectedRevisionText: string,
  rawConfigJsonString: string,
  signal: AbortSignal,
): Promise<GroupPolicy> {
  const body = `{"expected_revision":"${expectedRevisionText}","config":${rawConfigJsonString.trim()}}`
  return readGroupPolicy(
    await client.request(`/api/groups/${id}/policy`, {
      method: 'PUT',
      body,
      headers: { 'Content-Type': 'application/json' },
      signal,
    }),
  )
}

export async function saveCredentialPolicy(
  client: ApiClient,
  groupId: number,
  credentialId: number,
  expectedRevisionText: string,
  rawConfigJsonString: string,
  signal: AbortSignal,
): Promise<CredentialPolicy> {
  const body = `{"expected_revision":"${expectedRevisionText}","config":${rawConfigJsonString.trim()}}`
  return readCredentialPolicy(
    await client.request(`/api/groups/${groupId}/credentials/${credentialId}/policy`, {
      method: 'PUT',
      body,
      headers: { 'Content-Type': 'application/json' },
      signal,
    }),
  )
}

export interface PolicyConditionNode {
  kind: 'all' | 'any' | 'not' | 'param' | 'time_window'
  fact?: string
  truth: 'true' | 'false' | 'unknown'
  unknown_reason?: string
  children?: PolicyConditionNode[]
}

export interface PolicyPreviewRule {
  rule_id: string
  name_snapshot: string
  domain: 'scheduling' | 'pricing'
  enabled: boolean
  status: 'hit' | 'miss' | 'skipped_unknown' | 'disabled'
  binding_scope: 'group' | 'credential'
  provenance: 'draft' | 'saved'
  revision_text: string
  condition: PolicyConditionNode
  action: {
    type: 'exclude_candidate' | 'multiply_price'
    factor?: string
    multiplier?: string
  }
}

export interface PolicyPreviewPricingMatch {
  rule_id: string
  name_snapshot: string
  domain: string
  factor: string
  multiplier: string
  binding_scope: string
  provenance: string
  revision_text: string
}

export interface PolicyPreviewPricingResult {
  matches: PolicyPreviewPricingMatch[]
  factors: string[]
  cumulative_multiplier: string
}

export interface PolicyPreviewSchedulingResult {
  excluded: boolean
  reason?: {
    rule_id: string
    name_snapshot: string
    domain: string
  } | null
}

export interface PolicyPreviewTarget {
  upstream_model: string
  available: boolean
  hard_filter_state?: string
  group_rules: PolicyPreviewRule[]
  credential_rules: PolicyPreviewRule[]
  scheduling: PolicyPreviewSchedulingResult
  pricing: PolicyPreviewPricingResult
}

export interface PolicyPreviewCandidate {
  credential_id: number
  credential_name: string
  credential_version?: string
  identity_generation?: string
  targets: PolicyPreviewTarget[]
}

export interface PolicyPreviewResponse {
  snapshot_revision_text?: string
  server_time: string
  server_time_zone_offset: string
  simulated_time?: string
  caveat_codes: string[]
  candidates: PolicyPreviewCandidate[]
}

// Server output is trusted; project core DTO shapes without duplicating backend compiler validation.
export function readPolicyPreviewResponse(raw: unknown): PolicyPreviewResponse {
  const data = record(raw)
  const snapshotRevisionText =
    data.snapshot_revision_text !== undefined && data.snapshot_revision_text !== null
      ? text(data.snapshot_revision_text)
      : undefined
  const serverTime = text(data.server_time)
  const serverTimeZoneOffset = text(data.server_time_zone_offset)
  const simulatedTime =
    typeof data.simulated_time === 'string' && data.simulated_time.trim() !== ''
      ? data.simulated_time
      : undefined
  const caveatCodes = list(data.caveat_codes).map((c) => text(c))

  function parseConditionNode(cRaw: unknown): PolicyConditionNode {
    const c = record(cRaw)
    const kind = text(c.kind) as PolicyConditionNode['kind']
    const truth = text(c.truth) as PolicyConditionNode['truth']
    const fact = typeof c.fact === 'string' ? c.fact : undefined
    const unknownReason = typeof c.unknown_reason === 'string' ? c.unknown_reason : undefined
    const children = Array.isArray(c.children) ? c.children.map(parseConditionNode) : undefined

    return {
      kind,
      truth,
      fact,
      unknown_reason: unknownReason,
      children,
    }
  }

  const candidates = list(data.candidates).map((candRaw) => {
    const cand = record(candRaw)
    const credentialId = integer(cand.credential_id, 1)
    const credentialName = text(cand.credential_name)
    const credentialVersion =
      cand.credential_version !== undefined && cand.credential_version !== null
        ? text(cand.credential_version)
        : undefined
    const identityGeneration =
      cand.identity_generation !== undefined && cand.identity_generation !== null
        ? text(cand.identity_generation)
        : undefined

    const targets = list(cand.targets).map((targetRaw) => {
      const target = record(targetRaw)
      const upstreamModel = text(target.upstream_model)
      const available = boolean(target.available)
      const hardFilterState =
        typeof target.hard_filter_state === 'string' ? target.hard_filter_state : ''

      const parseRule = (rRaw: unknown): PolicyPreviewRule => {
        const r = record(rRaw)
        const act = record(r.action)
        return {
          rule_id: text(r.rule_id),
          name_snapshot: typeof r.name_snapshot === 'string' ? r.name_snapshot : '',
          domain: text(r.domain) as PolicyPreviewRule['domain'],
          enabled: boolean(r.enabled),
          status: text(r.status) as PolicyPreviewRule['status'],
          binding_scope: text(r.binding_scope) as PolicyPreviewRule['binding_scope'],
          provenance: text(r.provenance) as PolicyPreviewRule['provenance'],
          revision_text: text(r.revision_text),
          condition: parseConditionNode(r.condition),
          action: {
            type: text(act.type) as PolicyPreviewRule['action']['type'],
            factor: typeof act.factor === 'string' ? act.factor : undefined,
            multiplier: typeof act.multiplier === 'string' ? act.multiplier : undefined,
          },
        }
      }

      const groupRules = list(target.group_rules).map(parseRule)
      const credentialRules = list(target.credential_rules).map(parseRule)

      const sched = record(target.scheduling)
      const excluded = boolean(sched.excluded)
      let reason: PolicyPreviewSchedulingResult['reason'] = null
      if (sched.reason && typeof sched.reason === 'object') {
        const r = record(sched.reason)
        reason = {
          rule_id: text(r.rule_id),
          name_snapshot: typeof r.name_snapshot === 'string' ? r.name_snapshot : '',
          domain: text(r.domain),
        }
      }

      const pricingRaw = record(target.pricing)
      const matches = list(pricingRaw.matches).map((mRaw) => {
        const m = record(mRaw)
        return {
          rule_id: text(m.rule_id),
          name_snapshot: typeof m.name_snapshot === 'string' ? m.name_snapshot : '',
          domain: text(m.domain),
          factor: text(m.factor),
          multiplier: text(m.multiplier),
          binding_scope: text(m.binding_scope),
          provenance: text(m.provenance),
          revision_text: text(m.revision_text),
        }
      })
      const factors = list(pricingRaw.factors).map((f) => text(f))
      const cumulativeMultiplier = text(pricingRaw.cumulative_multiplier)

      return {
        upstream_model: upstreamModel,
        available,
        hard_filter_state: hardFilterState,
        group_rules: groupRules,
        credential_rules: credentialRules,
        scheduling: {
          excluded,
          reason,
        },
        pricing: {
          matches,
          factors,
          cumulative_multiplier: cumulativeMultiplier,
        },
      }
    })

    return {
      credential_id: credentialId,
      credential_name: credentialName,
      credential_version: credentialVersion,
      identity_generation: identityGeneration,
      targets,
    }
  })

  return {
    snapshot_revision_text: snapshotRevisionText,
    server_time: serverTime,
    server_time_zone_offset: serverTimeZoneOffset,
    simulated_time: simulatedTime,
    caveat_codes: caveatCodes,
    candidates,
  }
}

export async function previewGroupPolicy(
  client: ApiClient,
  groupId: number,
  requestModel: string,
  simulatedTime: string | null,
  rawConfigJsonString: string | null,
  signal: AbortSignal,
): Promise<PolicyPreviewResponse> {
  const parts: string[] = [`"request_model":${JSON.stringify(requestModel)}`]
  if (simulatedTime) {
    parts.push(`"simulated_time":${JSON.stringify(simulatedTime)}`)
  }
  if (rawConfigJsonString && rawConfigJsonString.trim()) {
    parts.push(`"config":${rawConfigJsonString.trim()}`)
  }
  const body = `{${parts.join(',')}}`
  const raw = await client.request<unknown>(`/api/groups/${groupId}/policy/preview`, {
    method: 'POST',
    body,
    headers: { 'Content-Type': 'application/json' },
    signal,
  })
  return readPolicyPreviewResponse(raw)
}

export async function previewCredentialPolicy(
  client: ApiClient,
  groupId: number,
  credentialId: number,
  requestModel: string,
  simulatedTime: string | null,
  rawConfigJsonString: string | null,
  signal: AbortSignal,
): Promise<PolicyPreviewResponse> {
  const parts: string[] = [`"request_model":${JSON.stringify(requestModel)}`]
  if (simulatedTime) {
    parts.push(`"simulated_time":${JSON.stringify(simulatedTime)}`)
  }
  if (rawConfigJsonString && rawConfigJsonString.trim()) {
    parts.push(`"config":${rawConfigJsonString.trim()}`)
  }
  const body = `{${parts.join(',')}}`
  const raw = await client.request<unknown>(
    `/api/groups/${groupId}/credentials/${credentialId}/policy/preview`,
    {
      method: 'POST',
      body,
      headers: { 'Content-Type': 'application/json' },
      signal,
    },
  )
  return readPolicyPreviewResponse(raw)
}

export interface PolicyParamDescriptor {
  key: string
  type: string
  unit?: string
  label: string
  description: string
  operators: string[]
  domains: string[]
  binding_scopes: string[]
  selector_constraint?: {
    scope: string
    window_seconds_required: boolean
  }
  reducers?: string[]
}

export interface PolicyPredicateDescriptor {
  name: string
  label: string
  description: string
  domains: string[]
}

export interface PolicyActionDescriptor {
  type: string
  domain: string
  label: string
  description: string
  fields: string[]
}

export interface PolicyCapabilities {
  account_wise: boolean
  group_aggregation: boolean
  fixed_recovery: string
  live_dynamic_pricing: boolean
}

export interface PolicyDiscovery {
  parameters: PolicyParamDescriptor[]
  predicates: PolicyPredicateDescriptor[]
  actions: PolicyActionDescriptor[]
  capabilities: PolicyCapabilities
}

// Server output is trusted; project core DTO shapes without duplicating backend compiler validation.
export function readPolicyDiscovery(raw: unknown): PolicyDiscovery {
  const data = record(raw)
  const parameters = list(data.parameters).map((pRaw) => {
    const p = record(pRaw)
    let selectorConstraint: PolicyParamDescriptor['selector_constraint']
    if (p.selector_constraint && typeof p.selector_constraint === 'object') {
      const sc = record(p.selector_constraint)
      selectorConstraint = {
        scope: text(sc.scope),
        window_seconds_required: boolean(sc.window_seconds_required),
      }
    }
    return {
      key: text(p.key),
      type: text(p.type),
      unit: typeof p.unit === 'string' ? p.unit : undefined,
      label: text(p.label),
      description: text(p.description),
      operators: list(p.operators).map((op) => text(op)),
      domains: list(p.domains).map((d) => text(d)),
      binding_scopes: list(p.binding_scopes).map((bs) => text(bs)),
      selector_constraint: selectorConstraint,
      reducers: Array.isArray(p.reducers) ? list(p.reducers).map((r) => text(r)) : undefined,
    }
  })
  const predicates = list(data.predicates).map((predRaw) => {
    const pred = record(predRaw)
    return {
      name: text(pred.name),
      label: text(pred.label),
      description: text(pred.description),
      domains: list(pred.domains).map((d) => text(d)),
    }
  })
  const actions = list(data.actions).map((actRaw) => {
    const act = record(actRaw)
    return {
      type: text(act.type),
      domain: text(act.domain),
      label: text(act.label),
      description: text(act.description),
      fields: list(act.fields).map((f) => text(f)),
    }
  })
  const caps = record(data.capabilities)
  const capabilities: PolicyCapabilities = {
    account_wise: boolean(caps.account_wise),
    group_aggregation: boolean(caps.group_aggregation),
    fixed_recovery: text(caps.fixed_recovery),
    live_dynamic_pricing: boolean(caps.live_dynamic_pricing),
  }
  return {
    parameters,
    predicates,
    actions,
    capabilities,
  }
}

export async function getPolicyDiscovery(
  client: ApiClient,
  signal: AbortSignal,
): Promise<PolicyDiscovery> {
  const raw = await client.request<unknown>('/api/policy/discovery', { signal })
  return readPolicyDiscovery(raw)
}
