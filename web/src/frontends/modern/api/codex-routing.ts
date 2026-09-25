import type { ApiClient } from '@shared/http/client'
import { InvalidResponseError } from '@shared/http/errors'
import { boolean, integer, list, oneOf, record, text } from './response'

const verdicts = ['empty', 'probing', 'pinned', 'rotated', 'stale', 'degraded'] as const
const actions = ['inject', 'reuse', 'write', 'rotated', 'empty', 'skip', ''] as const
const kinds = ['probe', 'business'] as const

export type CodexRoutingVerdict = (typeof verdicts)[number]
export type CodexRoutingAction = Exclude<(typeof actions)[number], ''>
export type CodexRoutingKind = (typeof kinds)[number]

export interface CodexRoutingCounts {
  empty: number
  probing: number
  pinned: number
  rotated: number
  stale: number
  degraded: number
}
export interface CodexRoutingCredential {
  id: number
  groupID: number
  groupName: string
  region: string
  verdict: CodexRoutingVerdict
  lastAction: string
  injectSuccess: boolean
  expiresAt: number | null
  ttlSeconds: number | null
  probeColo: string
  edgeRegion: string
  bufferingEnabled: boolean | null
  fasterModel: string
  lastModel: string
  lastReportedModel: string
  lastProbeAt: number | null
  lastBusinessAt: number | null
  edgeIP: string
  candyOK: boolean
  ticketLen: number
  ticketTTLSeconds: number | null
  ticketExpiresAt: number | null
  servedModel: string
  proxyRegion: string
}
export interface CodexRoutingStatus {
  enabled: boolean
  probeProxyConfigured: boolean
  probeRelayConfigured: boolean
  edgeIP: string
  targetGateway: string
  transparent: boolean
  mint: boolean
  maxRotates: number
  probeRegions: string[]
  ticketTTLSeconds: number
  observedAt: number
  counts: CodexRoutingCounts
  credentials: CodexRoutingCredential[]
}
export interface CodexRoutingEvent {
  time: number
  kind: CodexRoutingKind
  action: string
  credentialID: number
  model: string
  region: string
  previousRegion: string
  cookieNames: string[]
  bufferingEnabled: boolean | null
  fasterModel: string
  turnStateLen: number
  cfColo: string
  edgeRegion: string
  proxyRegion: string
  success: boolean
  statusCode: number
}

function optionalText(value: unknown): string {
  if (value === undefined || value === null) return ''
  return text(value)
}
function optionalInteger(value: unknown): number | null {
  if (value === undefined || value === null) return null
  return integer(value)
}
function optionalBoolean(value: unknown): boolean | null {
  if (value === undefined || value === null) return null
  return boolean(value)
}

function counts(value: unknown): CodexRoutingCounts {
  const row = record(value)
  return {
    empty: integer(row.empty),
    probing: integer(row.probing),
    pinned: integer(row.pinned),
    rotated: integer(row.rotated),
    stale: integer(row.stale),
    degraded: integer(row.degraded),
  }
}

function credential(value: unknown): CodexRoutingCredential {
  const row = record(value)
  return {
    id: integer(row.credential_id, 1),
    groupID: row.group_id === undefined ? 0 : integer(row.group_id),
    groupName: optionalText(row.group_name),
    region: optionalText(row.region),
    verdict: oneOf(row.verdict, verdicts),
    lastAction: optionalText(row.last_action),
    injectSuccess: boolean(row.inject_success),
    expiresAt: optionalInteger(row.expires_at_ms),
    ttlSeconds: optionalInteger(row.ttl_seconds),
    probeColo: optionalText(row.probe_colo),
    edgeRegion: optionalText(row.edge_region),
    bufferingEnabled: optionalBoolean(row.buffering_enabled),
    fasterModel: optionalText(row.faster_model),
    lastModel: optionalText(row.last_model),
    lastReportedModel: optionalText(row.last_reported_model),
    lastProbeAt: optionalInteger(row.last_probe_at_ms),
    lastBusinessAt: optionalInteger(row.last_business_at_ms),
    edgeIP: optionalText(row.edge_ip),
    candyOK: optionalBoolean(row.candy_ok) === true,
    ticketLen: row.ticket_len === undefined ? 0 : integer(row.ticket_len),
    ticketTTLSeconds: optionalInteger(row.ticket_ttl_seconds),
    ticketExpiresAt: optionalInteger(row.ticket_expires_at_ms),
    servedModel: optionalText(row.served_model),
    proxyRegion: optionalText(row.proxy_region),
  }
}

export function readCodexRoutingStatus(value: unknown): CodexRoutingStatus {
  const row = record(value)
  return {
    enabled: boolean(row.enabled),
    probeProxyConfigured: boolean(row.probe_proxy_configured),
    probeRelayConfigured: optionalBoolean(row.probe_relay_configured) === true,
    edgeIP: optionalText(row.edge_ip),
    targetGateway: optionalText(row.target_gateway),
    transparent: boolean(row.transparent),
    mint: boolean(row.mint),
    maxRotates: row.max_rotates === undefined ? 0 : integer(row.max_rotates),
    probeRegions:
      row.probe_regions === undefined || row.probe_regions === null
        ? []
        : list(row.probe_regions).map((item) => text(item)),
    ticketTTLSeconds: row.ticket_ttl_seconds === undefined ? 0 : integer(row.ticket_ttl_seconds),
    observedAt: integer(row.observed_at_ms),
    counts: counts(row.counts),
    credentials: list(row.credentials).map(credential),
  }
}

function eventItem(value: unknown): CodexRoutingEvent {
  const row = record(value)
  const kind = oneOf(row.kind, kinds)
  const action = optionalText(row.action)
  if (action && !actions.includes(action as (typeof actions)[number])) {
    throw new InvalidResponseError()
  }
  return {
    time: integer(row.time_ms),
    kind,
    action,
    credentialID: integer(row.credential_id, 1),
    model: optionalText(row.model),
    region: optionalText(row.region),
    previousRegion: optionalText(row.previous_region),
    cookieNames:
      row.cookie_names === undefined ? [] : list(row.cookie_names).map((item) => text(item)),
    bufferingEnabled: optionalBoolean(row.buffering_enabled),
    fasterModel: optionalText(row.faster_model),
    turnStateLen: optionalInteger(row.turn_state_len) ?? 0,
    cfColo: optionalText(row.cf_colo),
    edgeRegion: optionalText(row.edge_region),
    proxyRegion: optionalText(row.proxy_region),
    success: boolean(row.success),
    statusCode: optionalInteger(row.status_code) ?? 0,
  }
}

export async function getCodexRoutingStatus(
  client: ApiClient,
  signal: AbortSignal,
): Promise<CodexRoutingStatus> {
  return readCodexRoutingStatus(await client.request('/api/codex-routing/status', { signal }))
}

export async function getCodexRoutingEvents(
  client: ApiClient,
  signal: AbortSignal,
): Promise<CodexRoutingEvent[]> {
  const row = record(await client.request('/api/codex-routing/events', { signal }))
  return list(row.items).map(eventItem)
}

export async function probeCodexRouting(
  client: ApiClient,
  credentialID: number,
  signal?: AbortSignal,
): Promise<CodexRoutingStatus> {
  return readCodexRoutingStatus(
    await client.request('/api/codex-routing/probe', {
      method: 'POST',
      json: { credential_id: credentialID },
      signal,
    }),
  )
}

export async function clearCodexRouting(
  client: ApiClient,
  credentialID: number,
  signal?: AbortSignal,
): Promise<CodexRoutingStatus> {
  return readCodexRoutingStatus(
    await client.request('/api/codex-routing/clear', {
      method: 'POST',
      json: { credential_id: credentialID, confirm: true },
      signal,
    }),
  )
}
