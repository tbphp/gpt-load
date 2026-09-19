import type { ApiClient } from '@shared/http/client'
import { InvalidResponseError } from '@shared/http/errors'
import { boolean, integer, list, record, text } from './response'

export interface BalanceEntry {
  currency: string
  total: string
  granted: string | null
  toppedUp: string | null
}

export interface CredentialBalance {
  credentialId: number
  supported: boolean
  available: boolean
  balances: BalanceEntry[]
}

export interface GroupBalanceTotal {
  groupId: number
  channelId: string
  supported: boolean
  known: boolean
  totals: BalanceEntry[]
}

function optionalText(value: unknown): string | null {
  return value === undefined || value === null ? null : text(value)
}

function readEntry(value: unknown): BalanceEntry {
  const entry = record(value)
  return {
    currency: text(entry.currency),
    total: text(entry.total_balance),
    granted: optionalText(entry.granted_balance),
    toppedUp: optionalText(entry.topped_up_balance),
  }
}

function readCredentialBalance(value: unknown): CredentialBalance {
  const data = record(value)
  return {
    credentialId: integer(data.credential_id, 1),
    supported: boolean(data.supported),
    available: boolean(data.available),
    balances: list(data.balances).map(readEntry),
  }
}

export const groupBalanceTotalsKey = (refresh: boolean) =>
  ['modern', 'group-balance-totals', refresh] as const
export const groupCredentialBalancesKey = (groupId: number, refresh: boolean) =>
  ['modern', 'group-credential-balances', groupId, refresh] as const

/** Formats a balance list as compact text, e.g. "CNY 0.87 · USD 5.50". */
export function formatBalanceEntries(entries: BalanceEntry[]): string {
  return entries.map((entry) => `${entry.currency} ${entry.total}`).join(' · ')
}

export async function getGroupBalanceTotals(
  client: ApiClient,
  refresh: boolean,
  signal: AbortSignal,
): Promise<Map<number, GroupBalanceTotal>> {
  const data = record(
    await client.request(`/api/balances/groups${refresh ? '?refresh=1' : ''}`, { signal }),
  )
  const items = list(data.items).map((value) => {
    const item = record(value)
    return {
      groupId: integer(item.group_id, 1),
      channelId: text(item.channel_id),
      supported: boolean(item.supported),
      known: boolean(item.known),
      totals: list(item.totals).map(readEntry),
    }
  })
  return new Map(items.map((item) => [item.groupId, item]))
}

export async function getGroupCredentialBalances(
  client: ApiClient,
  groupId: number,
  refresh: boolean,
  signal: AbortSignal,
): Promise<Map<number, CredentialBalance>> {
  const data = record(
    await client.request(
      `/api/groups/${groupId}/credentials/balances${refresh ? '?refresh=1' : ''}`,
      { signal },
    ),
  )
  if (integer(data.group_id, 1) !== groupId) throw new InvalidResponseError()
  const items = list(data.items).map(readCredentialBalance)
  return new Map(items.map((item) => [item.credentialId, item]))
}
