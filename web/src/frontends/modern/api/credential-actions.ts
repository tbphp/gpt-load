import type { ApiClient } from '@shared/http/client'
import { InvalidResponseError } from '@shared/http/errors'
import type { ProxyOverride } from './group-create'
import { readCredential } from './group-detail'
import { readObservation } from './credential-observation'
import { boolean, integer, list, oneOf, record, text } from './response'

export const credentialDetailKey = (group: number, id: number) =>
  ['modern', 'credential-detail', group, id] as const
export async function getCredentialDetail(
  client: ApiClient,
  group: number,
  id: number,
  signal: AbortSignal,
) {
  const data = record(
    await client.request(`/api/modern/groups/${group}/credentials/${id}`, { signal }),
  )
  const item = readCredential(data.credential)
  if (item.id !== id) throw new InvalidResponseError()
  return { ...item, observation: readObservation(data.observation) ?? item.observation }
}
export async function updateCredential(
  client: ApiClient,
  group: number,
  id: number,
  patch: { weight_manual?: number | null; proxy?: ProxyOverride | null },
  signal: AbortSignal,
) {
  const row = readCredential(
    await client.request(`/api/groups/${group}/credentials/${id}`, {
      method: 'PUT',
      json: patch,
      signal,
    }),
  )
  return Object.hasOwn(patch, 'weight_manual') ? { ...row, weightManual: patch.weight_manual } : row
}

export async function exportAllCredentials(client: ApiClient, group: number, signal: AbortSignal) {
  const data = record(
    await client.request(`/api/groups/${group}/credentials/download-all`, {
      method: 'POST',
      json: {},
      signal,
    }),
  )
  const count = integer(data.credential_count)
  const files = list(data.files).map((value) => {
    const file = record(value)
    const filename = text(file.filename)
    const plain = Object.hasOwn(file, 'content')
    if (
      !(plain ? /^[a-z0-9][a-z0-9._-]{0,191}\.txt$/u : /^[a-z0-9][a-z0-9._-]{0,191}\.json$/u).test(
        filename,
      )
    )
      throw new InvalidResponseError()
    return {
      filename,
      content: plain ? text(file.content) : JSON.stringify(record(file.credential), null, 2),
      type: plain ? 'text/plain;charset=utf-8' : 'application/json;charset=utf-8',
    }
  })
  if (
    files.some((file) => file.type.startsWith('text/'))
      ? files.length !== 1
      : files.length !== count
  )
    throw new InvalidResponseError()
  return { count, files }
}
export async function runCredentialAction(
  client: ApiClient,
  group: number,
  id: number,
  action: 'restore' | 'refresh',
  signal: AbortSignal,
) {
  return readCredential(
    await client.request(`/api/groups/${group}/credentials/${id}/${action}`, {
      method: 'POST',
      json: {},
      signal,
    }),
  )
}
export async function refreshCredentialQuota(
  client: ApiClient,
  group: number,
  id: number,
  signal: AbortSignal,
) {
  return readObservation(
    await client.request(`/api/groups/${group}/credentials/${id}/observation-refresh`, {
      method: 'POST',
      json: {},
      signal,
    }),
  )
}
export async function revealCredential(
  client: ApiClient,
  group: number,
  id: number,
  signal: AbortSignal,
): Promise<string> {
  const data = record(
    await client.request(`/api/groups/${group}/credentials/${id}/reveal`, {
      method: 'POST',
      signal,
    }),
  )
  if (integer(data.credential_id, 1) !== id) throw new InvalidResponseError()
  const fields = Object.fromEntries(
    Object.entries(record(data.credential)).map(([key, value]) => [key, text(value)]),
  )
  const values = Object.values(fields)
  if (!values.length) throw new InvalidResponseError()
  return values.length === 1 ? values[0]! : JSON.stringify(fields, null, 2)
}
export async function exportCredential(
  client: ApiClient,
  group: number,
  id: number,
  signal: AbortSignal,
) {
  const data = record(
    await client.request(`/api/groups/${group}/credentials/${id}/download`, {
      method: 'POST',
      json: {},
      signal,
    }),
  )
  const filename = text(data.filename)
  if (!/^[a-z0-9][a-z0-9._-]{0,191}\.json$/u.test(filename)) throw new InvalidResponseError()
  return { filename, content: JSON.stringify(record(data.credential), null, 2) }
}
export async function resetCredentialQuota(
  client: ApiClient,
  group: number,
  id: number,
  key: string,
  signal: AbortSignal,
) {
  const data = record(
    await client.request(`/api/groups/${group}/credentials/${id}/reset-credits/consume`, {
      method: 'POST',
      json: {},
      headers: { 'Idempotency-Key': key },
      signal,
    }),
  )
  oneOf(data.status, ['succeeded'])
  return {
    windows: integer(data.windows_reset),
    observation: readObservation(data.observation),
    pending: data.observation_pending === undefined ? false : boolean(data.observation_pending),
  }
}
export interface CredentialTestResult {
  outcome: 'passed' | 'failed' | 'inconclusive'
  latency: number
  model: string
  protocol: string
  reason: string | null
  proof: string | null
}
export async function testCredential(
  client: ApiClient,
  group: number,
  id: number,
  protocol: string,
  model: string,
  signal: AbortSignal,
): Promise<CredentialTestResult> {
  const data = record(
    await client.request(`/api/groups/${group}/credentials/${id}/test`, {
      method: 'POST',
      json: { protocol, model },
      signal,
    }),
  )
  return {
    outcome: oneOf(data.outcome, ['passed', 'failed', 'inconclusive']),
    latency: integer(data.latency_ms),
    model: text(data.model),
    protocol: text(data.protocol),
    reason: data.reason === null ? null : text(data.reason),
    proof:
      boolean(data.can_restore) && data.restore_proof !== null ? text(data.restore_proof) : null,
  }
}
export async function restoreTestedCredential(
  client: ApiClient,
  group: number,
  id: number,
  proof: string,
  signal: AbortSignal,
) {
  return readCredential(
    await client.request(`/api/groups/${group}/credentials/${id}/test/restore`, {
      method: 'POST',
      json: { restore_proof: proof },
      signal,
    }),
  )
}
