import type { ApiClient } from '@shared/http/client'
import { readAccessKeyRow, type AccessKeyRow } from './access-keys'
import { readCredential, type CredentialRow } from './group-detail'
import { readCredentialFilterKey } from './groups'
import { integer, list, record, text } from './response'

export interface HomeKey {
  id: number
  name: string
  mask: string
}
export interface HomeBase {
  observedAt: number
  startedAt: number
  version: string
  groups: number
  credentials: number
  available: number
  models: number
  keys: HomeKey[]
  currentKey: AccessKeyRow | null
}
export interface HomeAccount {
  key: string
  channelID: string
  channelName: string
  channelIcon: string
  channelMark: string
  groups: number
  availableGroups: number
  groupID: number | null
  credential: CredentialRow
}
export async function getHome(client: ApiClient, signal: AbortSignal): Promise<HomeBase> {
  const data = record(await client.request('/api/home', { signal }))
  const inventory = record(data.inventory)
  return {
    observedAt: integer(data.server_now_ms),
    startedAt: integer(data.started_at_ms),
    version: text(data.version),
    groups: integer(inventory.group_count),
    credentials: integer(inventory.credential_count),
    available: integer(inventory.available_credential_count),
    models: integer(inventory.model_count),
    keys: list(data.access_keys).map((value) => {
      const key = record(value)
      return {
        id: integer(key.id, 1),
        name: text(key.name),
        mask: text(key.masked_key),
      }
    }),
    currentKey: data.current_access_key == null ? null : readAccessKeyRow(data.current_access_key),
  }
}
export async function getHomeAccounts(client: ApiClient, signal: AbortSignal) {
  const data = record(await client.request('/api/home/subscription-accounts', { signal }))
  return {
    observedAt: integer(data.observed_at_ms),
    items: list(data.items).map((value): HomeAccount => {
      const account = record(value)
      return {
        key: readCredentialFilterKey(account.credential_key),
        channelID: text(account.channel_id),
        channelName: text(account.channel_name),
        channelIcon: text(account.channel_icon),
        channelMark: text(account.channel_mark),
        groups: integer(account.group_count),
        groupID: account.group_id == null ? null : integer(account.group_id, 1),
        availableGroups: integer(account.available_group_count),
        credential: readCredential(account.credential),
      }
    }),
  }
}

export interface HomeTrendPoint {
  startMs: number
  requests: number
  failures: number
}
export interface HomeStatistics {
  observedAt: number
  fromMs: number
  toMs: number
  requests: number
  failures: number
  series: HomeTrendPoint[]
}
export const homeStatisticsKey = ['modern', 'home', 'statistics'] as const
/* 首页只要 24 小时的请求量与失败数；排行留给用量统计页，不在这里重复一份。 */
export async function getHomeStatistics(
  client: ApiClient,
  signal: AbortSignal,
): Promise<HomeStatistics> {
  const data = record(await client.request('/api/home/statistics?range=24h', { signal }))
  const summary = record(data.summary)
  return {
    observedAt: integer(data.observed_at_ms),
    fromMs: integer(data.from_ms),
    toMs: integer(data.to_ms),
    requests: integer(summary.request_count),
    failures: integer(summary.failure_count),
    series: list(data.series).map((value) => {
      const point = record(value)
      return {
        startMs: integer(point.bucket_start_ms),
        requests: integer(point.request_count),
        failures: integer(point.failure_count),
      }
    }),
  }
}
