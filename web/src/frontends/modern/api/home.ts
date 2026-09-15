import type { ApiClient } from '@shared/http/client'
import { sortProtocols } from '@modern/i18n/protocols'
import { readAccessKeyRow, type AccessKeyRow } from './access-keys'
import { readCredential, type CredentialRow } from './group-detail'
import { integer, list, record, text } from './response'

export interface HomeKey {
  id: number
  name: string
  mask: string
  protocols: string[]
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
  channelID: string
  channelName: string
  channelIcon: string
  channelMark: string
  groups: number
  availableGroups: number
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
        protocols: sortProtocols(list(key.protocols).map(text)),
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
        channelID: text(account.channel_id),
        channelName: text(account.channel_name),
        channelIcon: text(account.channel_icon),
        channelMark: text(account.channel_mark),
        groups: integer(account.group_count),
        availableGroups: integer(account.available_group_count),
        credential: readCredential(account.credential),
      }
    }),
  }
}
